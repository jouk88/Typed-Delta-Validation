/*
loaddriver — minimal Fabric Gateway (Go) load driver for the typed-delta evaluation.

Drives a single hot key with concurrent debits and records, PER TX, the COMMIT-EVENT
validation code (NOT invoke/submit success): SubmitAsync returns after the tx is
submitted; commit.Status() blocks until the peer commits it and returns the
TxValidationCode. Latency = submit-start -> commit-status-received.

baselines: ours (typeddelta: Sub via endorsement-safe delta) | vanilla (RMW: Debit
reads then writes -> MVCC conflict on a hot key). Output: JSON summary + per-tx CSV.

Randomized-workload extension for E6 (backward-compatible; inert when -seed==0): when -seed>0 the driver
generates, sequentially and deterministically BEFORE the concurrent load loop, a
per-tx schedule of (amount, credit?) so the same seed always yields the same workload
and there is no shared-RNG data race across the load goroutines. amountMax picks the
per-tx amount uniformly from [1,amountMax]; creditRatio is the %% of txs that call the
credit function (Add for ours/nocheck, Credit for ht) instead of the debit function.
Chaincode is UNCHANGED — Add/Credit already exist in both chaincodes.
*/
package main

import (
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hyperledger/fabric-gateway/pkg/client"
	"github.com/hyperledger/fabric-gateway/pkg/identity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	cc := flag.String("cc", "ours", "ours|nocheck|vanilla|ht")
	count := flag.Int("count", 500, "number of debit txs")
	conc := flag.Int("concurrency", 50, "concurrent in-flight submits")
	key := flag.String("key", "hot", "the (single, hot) balance key; with -keys>1 used as a prefix")
	nkeys := flag.Int("keys", 1, "number of distinct hot keys; debits are spread uniformly (round-robin) across them")
	amount := flag.Int64("amount", 1, "debit amount per tx")
	revealRatio := flag.Int("revealRatio", 0, "ours only: %% of debits that take the read-modify-write reveal path (0-100); rest use the delta path")
	initBal := flag.Int64("initBalance", 100000000, "initial balance (large => no overdraft, isolates MVCC effect)")
	seed := flag.Int64("seed", 0, "E6 randomized stress: RNG seed (0=off => deterministic single-amount debits, backward compatible)")
	amountMax := flag.Int64("amountMax", 0, "E6: max random amount, per tx uniform in [1,amountMax] (0=use -amount)")
	creditRatio := flag.Int("creditRatio", 0, "E6: %% of txs that call the credit fn (Add/Credit) instead of debit (0-100)")
	out := flag.String("out", "run.json", "output JSON summary path (also writes <out>.csv)")
	cryptoPath := flag.String("crypto", `C:\develop\fabric-samples\test-network\organizations\peerOrganizations\org1.example.com`, "Org1 crypto dir")
	peerEndpoint := flag.String("peer", "localhost:7051", "peer gateway endpoint")
	peerHost := flag.String("peerHost", "peer0.org1.example.com", "peer TLS hostname override")
	mspID := flag.String("mspID", "Org1MSP", "msp id")
	channel := flag.String("channel", "mychannel", "channel")
	adminUser := flag.String("adminUser", "Admin@org1.example.com", "MSP user dir that seeds schema (must be an org admin; schema-admin gate)")
	loadUser := flag.String("loadUser", "User1@org1.example.com", "MSP user dir that submits the debit load (ordinary client)")
	flag.Parse()

	ccName := map[string]string{"ours": "typeddelta", "nocheck": "typeddelta", "vanilla": "vanilla", "ht": "highthroughput"}[*cc]
	if ccName == "" {
		usageExit("cc must be ours|nocheck|vanilla|ht")
	}
	// Fail fast (exit 2) on bad measurement parameters, BEFORE any network dial, so a
	// misconfigured run cannot silently produce garbage data.
	switch {
	case *count <= 0:
		usageExit("-count must be > 0")
	case *conc <= 0:
		usageExit("-concurrency must be > 0")
	case *nkeys <= 0:
		usageExit("-keys must be > 0")
	case *revealRatio < 0 || *revealRatio > 100:
		usageExit("-revealRatio must be in [0,100]")
	case *amount < 0:
		usageExit("-amount must be >= 0")
	case *amountMax < 0:
		usageExit("-amountMax must be >= 0")
	case *creditRatio < 0 || *creditRatio > 100:
		usageExit("-creditRatio must be in [0,100]")
	}

	conn, err := newGrpcConn(*cryptoPath, *peerEndpoint, *peerHost)
	must(err, "grpc")
	defer conn.Close()

	// Two identities over one TLS connection: the org ADMIN seeds schema (schema-admin
	// gate) while an ordinary USER submits the debit load — matching the paper's threat
	// model that an ordinary client cannot create or weaken a schema.
	connectAs := func(user string) *client.Gateway {
		id, ierr := newIdentity(*cryptoPath, *mspID, user)
		must(ierr, "identity "+user)
		sign, serr := newSign(*cryptoPath, user)
		must(serr, "sign "+user)
		gw, gerr := client.Connect(id, client.WithSign(sign), client.WithClientConnection(conn),
			client.WithEvaluateTimeout(30*time.Second), client.WithSubmitTimeout(30*time.Second),
			client.WithCommitStatusTimeout(2*time.Minute))
		must(gerr, "gateway connect "+user)
		return gw
	}
	adminGw := connectAs(*adminUser)
	defer adminGw.Close()
	userGw := connectAs(*loadUser)
	defer userGw.Close()
	adminContract := adminGw.GetNetwork(*channel).GetContract(ccName) // seed (schema-admin)
	userContract := userGw.GetNetwork(*channel).GetContract(ccName)   // load (ordinary client)

	// ---- the hot key set (1 key = single-key stress case; >1 = contention spread) ----
	keys := make([]string, *nkeys)
	for j := range keys {
		if *nkeys == 1 {
			keys[j] = *key // identical to the single-key path
		} else {
			keys[j] = fmt.Sprintf("%s-k%d", *key, j)
		}
	}

	// seedAll submits fn over every key concurrently (distinct keys => no MVCC conflict)
	// and waits — a barrier. Calling it once per phase preserves ordering across phases
	// (e.g. all SeedSchema commit before any PutPlain, so the schema is visible).
	seedAll := func(fn string, withVal bool) {
		var swg sync.WaitGroup
		ssem := make(chan struct{}, *conc)
		var serr int64
		for _, k := range keys {
			swg.Add(1)
			ssem <- struct{}{}
			go func(k string) {
				defer swg.Done()
				defer func() { <-ssem }()
				var e error
				if withVal {
					_, e = adminContract.Submit(fn, client.WithArguments(k, itoa(*initBal)))
				} else {
					_, e = adminContract.Submit(fn, client.WithArguments(k))
				}
				if e != nil {
					atomic.AddInt64(&serr, 1)
				}
			}(k)
		}
		swg.Wait()
		if serr > 0 {
			must(fmt.Errorf("%d/%d seed submits failed for %s", serr, len(keys), fn), "seed")
		}
	}

	// ---- init the hot key set (synchronous, must commit VALID) ----
	fmt.Printf("init %s keys=%d (prefix=%s) balance=%d\n", *cc, *nkeys, *key, *initBal)
	switch *cc {
	case "ours", "nocheck":
		seedFn := "SeedSchema"      // NONNEG (rejects overdraft)
		if *cc == "nocheck" {
			seedFn = "SeedRangeSchema" // full-range (never rejects) — no-check merge baseline
		}
		seedAll(seedFn, false)   // all schemas first (barrier)
		seedAll("PutPlain", true) // then all balances (schema now committed and visible)
	case "vanilla", "ht":
		seedAll("Set", true)
	}

	debitFn := map[string]string{"ours": "Sub", "nocheck": "Sub", "vanilla": "Debit", "ht": "Debit"}[*cc]
	// E6: credit fn already exists in both chaincodes (typeddelta Add, highthroughput Credit).
	creditFn := map[string]string{"ours": "Add", "nocheck": "Add", "ht": "Credit"}[*cc]

	// E6 per-tx schedule (amount + credit?). Generated sequentially from *seed BEFORE the
	// concurrent load loop => (1) deterministic per seed, (2) no shared-RNG data race across
	// the load goroutines. Inert when *seed==0 (schedule stays nil; original path is used).
	txAmounts := make([]int64, *count)
	txCredit := make([]bool, *count)
	for i := range txAmounts {
		txAmounts[i] = *amount
	}
	if *seed > 0 {
		rng := rand.New(rand.NewSource(*seed))
		for i := 0; i < *count; i++ {
			if *amountMax > 0 {
				txAmounts[i] = rng.Int63n(*amountMax) + 1
			}
			txCredit[i] = *creditRatio > 0 && rng.Intn(100) < *creditRatio
		}
	}

	// reveal schedule (ours only): a deterministic, evenly-interleaved subset of tx
	// indices take the read-modify-write path instead of the delta path. Reproducible
	// (no RNG): index i is a reveal iff ((i*R) % 100) < R, which spreads R%% of the
	// txs uniformly across the run. revealRatio is ignored for non-ours baselines.
	rr := *revealRatio
	if *cc != "ours" {
		rr = 0
	}
	isReveal := func(i int) bool { return rr > 0 && (i*rr)%100 < rr }

	// ---- load phase ----
	type rec struct {
		idx     int
		txid    string
		key     string // the hot key this tx targeted (for multi-key runs)
		code    string
		ok      bool
		submitErr bool
		reveal  bool
		credit  bool
		latMs   float64
	}
	recs := make([]rec, *count)
	var submitErrs int64
	sem := make(chan struct{}, *conc)
	var wg sync.WaitGroup
	t0 := time.Now()
	for i := 0; i < *count; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			k := keys[i%len(keys)] // uniform round-robin across the hot-key set
			fn := debitFn
			reveal := isReveal(i)
			if reveal {
				fn = "RevealDebit" // ordinary read-modify-write fallback (reads hot key)
			}
			credit := txCredit[i]
			if credit {
				fn = creditFn // E6: this tx credits (Add/Credit) instead of debiting
			}
			_, commit, err := userContract.SubmitAsync(fn, client.WithArguments(k, itoa(txAmounts[i])))
			if err != nil {
				atomic.AddInt64(&submitErrs, 1)
				recs[i] = rec{idx: i, key: k, submitErr: true, reveal: reveal, credit: credit, latMs: ms(time.Since(start))}
				return
			}
			txid := commit.TransactionID()
			status, err := commit.Status() // blocks until COMMIT; status.Code = validationCode
			lat := ms(time.Since(start))
			if err != nil {
				recs[i] = rec{idx: i, txid: txid, key: k, submitErr: true, reveal: reveal, credit: credit, latMs: lat}
				return
			}
			recs[i] = rec{idx: i, txid: txid, key: k, code: status.Code.String(), ok: status.Successful, reveal: reveal, credit: credit, latMs: lat}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(t0).Seconds()

	// ---- aggregate ----
	byCode := map[string]int{}
	var valid, mvccInvalid int
	var revealTotal, revealValid int
	var creditTotal int
	var lats []float64
	for _, r := range recs {
		if r.reveal {
			revealTotal++
		}
		if r.credit {
			creditTotal++
		}
		if r.submitErr {
			byCode["SUBMIT_ERROR"]++
			continue
		}
		byCode[r.code]++
		if r.ok {
			valid++
			if r.reveal {
				revealValid++
			}
		} else if r.code == "MVCC_READ_CONFLICT" || r.code == "PHANTOM_READ_CONFLICT" {
			mvccInvalid++
		}
		lats = append(lats, r.latMs)
	}
	sort.Float64s(lats)

	summary := map[string]any{
		"cc":                *cc,
		"chaincode":         ccName,
		"key":               *key,
		"keys":              *nkeys,
		"count":             *count,
		"concurrency":       *conc,
		"amount":            *amount,
		"initBalance":       *initBal,
		"seed":              *seed,
		"amountMax":         *amountMax,
		"credit_ratio":      *creditRatio,
		"credit_total":      creditTotal,
		"reveal_ratio":      rr,
		"reveal_total":      revealTotal,
		"reveal_valid":      revealValid,
		"mvcc_invalid":      mvccInvalid,
		"elapsed_s":         round(elapsed, 3),
		"submitted":         *count,
		"submit_errors":     submitErrs,
		"valid":             valid,
		"invalid":           *count - valid - int(submitErrs),
		"by_code":           byCode,
		"valid_goodput_tps": round(float64(valid)/elapsed, 2),
		"submitted_tps":     round(float64(*count)/elapsed, 2),
		"p50_ms":            pct(lats, 50),
		"p95_ms":            pct(lats, 95),
		"p99_ms":            pct(lats, 99),
		"note":              "safe goodput == valid goodput for ours/vanilla (prefix violations expected 0; confirm via prefixanalyzer)",
	}
	js, _ := json.MarshalIndent(summary, "", "  ")
	if err := os.WriteFile(*out, js, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: write summary %s: %v\n", *out, err)
		os.Exit(1) // never let a measurement run report success while silently losing data
	}
	fmt.Println(string(js))

	// per-tx CSV (for analyzer cross-check / txid list). `credit` appended last so the
	// existing column order (idx,txid,code,ok,submitErr,reveal,latMs,key) is unchanged —
	// prefixanalyzer / fig2 parsers that index by position keep working.
	var csv []byte
	csv = append(csv, []byte("idx,txid,code,ok,submitErr,reveal,latMs,key,credit\n")...)
	for _, r := range recs {
		csv = append(csv, []byte(fmt.Sprintf("%d,%s,%s,%t,%t,%t,%.2f,%s,%t\n", r.idx, r.txid, r.code, r.ok, r.submitErr, r.reveal, r.latMs, r.key, r.credit))...)
	}
	if err := os.WriteFile(*out+".csv", csv, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: write per-tx CSV %s.csv: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s and %s.csv\n", *out, *out)
}

func newGrpcConn(cryptoPath, endpoint, host string) (*grpc.ClientConn, error) {
	tlsCertPath := filepath.Join(cryptoPath, "peers", "peer0.org1.example.com", "tls", "ca.crt")
	pem, err := os.ReadFile(tlsCertPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("failed to add TLS CA cert")
	}
	creds := credentials.NewClientTLSFromCert(pool, host)
	return grpc.NewClient(endpoint, grpc.WithTransportCredentials(creds))
}

func newIdentity(cryptoPath, mspID, user string) (*identity.X509Identity, error) {
	certDir := filepath.Join(cryptoPath, "users", user, "msp", "signcerts")
	pem, err := readFirstFile(certDir)
	if err != nil {
		return nil, err
	}
	cert, err := identity.CertificateFromPEM(pem)
	if err != nil {
		return nil, err
	}
	return identity.NewX509Identity(mspID, cert)
}

func newSign(cryptoPath, user string) (identity.Sign, error) {
	keyDir := filepath.Join(cryptoPath, "users", user, "msp", "keystore")
	pem, err := readFirstFile(keyDir)
	if err != nil {
		return nil, err
	}
	pk, err := identity.PrivateKeyFromPEM(pem)
	if err != nil {
		return nil, err
	}
	return identity.NewPrivateKeySign(pk)
}

func readFirstFile(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			return os.ReadFile(filepath.Join(dir, e.Name()))
		}
	}
	return nil, fmt.Errorf("no file in %s", dir)
}

func ms(d time.Duration) float64    { return float64(d.Microseconds()) / 1000.0 }
func round(f float64, n int) float64 { p := 1.0; for i := 0; i < n; i++ { p *= 10 }; return float64(int64(f*p+0.5)) / p }
func itoa(n int64) string            { return fmt.Sprintf("%d", n) }
func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", what, err)
		os.Exit(1)
	}
}

// usageExit reports a CLI misuse to stderr and exits 2 (measurement-artifact hygiene:
// a bad parameter must fail fast, not produce silently-wrong data).
func usageExit(msg string) {
	fmt.Fprintln(os.Stderr, "usage error: "+msg)
	flag.Usage()
	os.Exit(2)
}
func pct(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := (p * len(sorted)) / 100
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return round(sorted[i], 2)
}
