package main

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/hyperledger/fabric-chaincode-go/shim"
	pb "github.com/hyperledger/fabric-protos-go/peer"
)

const (
	typeID          = "balance_int64"
	defaultVersion  = "v1"
	schemaKeyPrefix = "typeddelta~schema~"
)

type storedSchema struct {
	TypeID     string  `json:"type_id"`
	AllowedOps []int32 `json:"allowed_ops"`
	Invariant  string  `json:"invariant"`
	Lo         int64   `json:"lo,omitempty"`
	Hi         int64   `json:"hi,omitempty"`
	Version    string  `json:"version"`
}

func schemaStorageKey(dataKey string) string {
	return schemaKeyPrefix + base64.RawURLEncoding.EncodeToString([]byte(dataKey))
}

func int64BE(n int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(n))
	return b
}

func decodeBE(b []byte) (int64, error) {
	if len(b) != 8 {
		return 0, fmt.Errorf("expected 8-byte int64, got %d bytes", len(b))
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}

var allowedSchemaAdminOrgs = map[string]bool{"org1.example.com": true, "org2.example.com": true}

func requireSchemaAdmin(stub shim.ChaincodeStubInterface) error {
	creator, err := stub.GetCreator()
	if err != nil {
		return fmt.Errorf("schema-admin check: %v", err)
	}
	i := bytes.Index(creator, []byte("-----BEGIN"))
	if i < 0 {
		return fmt.Errorf("schema write denied: no X.509 identity in creator")
	}
	block, _ := pem.Decode(creator[i:])
	if block == nil {
		return fmt.Errorf("schema write denied: malformed identity certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("schema write denied: %v", err)
	}
	isAdmin := false
	for _, ou := range cert.Subject.OrganizationalUnit {
		if ou == "admin" {
			isAdmin = true
			break
		}
	}
	if !isAdmin {
		return fmt.Errorf("schema write denied: identity %q lacks admin OU", cert.Subject.CommonName)
	}
	org := ""
	if at := strings.LastIndex(cert.Subject.CommonName, "@"); at >= 0 {
		org = cert.Subject.CommonName[at+1:]
	}
	if !allowedSchemaAdminOrgs[org] {
		return fmt.Errorf("schema write denied: org %q is not a schema admin", org)
	}
	return nil
}

func reservedKey(key string) bool {
	return strings.HasPrefix(key, schemaKeyPrefix)
}

type TypedDeltaCC struct{}

func (t *TypedDeltaCC) Init(stub shim.ChaincodeStubInterface) pb.Response {
	return shim.Success(nil)
}

func (t *TypedDeltaCC) Invoke(stub shim.ChaincodeStubInterface) pb.Response {
	fn, args := stub.GetFunctionAndParameters()
	switch fn {
	case "SeedSchema":
		return t.seedSchema(stub, args)
	case "SeedRangeSchema":
		return t.seedRangeSchema(stub, args)
	case "Add":
		return t.delta(stub, args, shim.DeltaAdd)
	case "Sub":
		return t.delta(stub, args, shim.DeltaSub)
	case "Transfer":
		return t.transfer(stub, args)
	case "PutPlain":
		return t.putPlain(stub, args)
	case "RevealDebit":
		return t.revealDebit(stub, args)
	case "Get":
		return t.get(stub, args)
	default:
		return shim.Error("unknown function: " + fn)
	}
}

func (t *TypedDeltaCC) seedSchema(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) < 1 || len(args) > 2 {
		return shim.Error("usage: SeedSchema(key [,version])")
	}
	if err := requireSchemaAdmin(stub); err != nil {
		return shim.Error(err.Error())
	}
	if reservedKey(args[0]) {
		return shim.Error("key uses the reserved schema prefix")
	}
	ver := defaultVersion
	if len(args) == 2 {
		ver = args[1]
	}
	s := storedSchema{
		TypeID:     typeID,
		AllowedOps: []int32{1, 2},
		Invariant:  "NONNEG",
		Version:    ver,
	}
	js, err := json.Marshal(s)
	if err != nil {
		return shim.Error(err.Error())
	}
	if err := stub.PutState(schemaStorageKey(args[0]), js); err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(nil)
}

func (t *TypedDeltaCC) seedRangeSchema(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 1 {
		return shim.Error("usage: SeedRangeSchema(key)")
	}
	if err := requireSchemaAdmin(stub); err != nil {
		return shim.Error(err.Error())
	}
	if reservedKey(args[0]) {
		return shim.Error("key uses the reserved schema prefix")
	}
	s := storedSchema{
		TypeID:     typeID,
		AllowedOps: []int32{1, 2},
		Invariant:  "RANGE",
		Lo:         math.MinInt64,
		Hi:         math.MaxInt64,
		Version:    defaultVersion,
	}
	js, err := json.Marshal(s)
	if err != nil {
		return shim.Error(err.Error())
	}
	if err := stub.PutState(schemaStorageKey(args[0]), js); err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(nil)
}

func (t *TypedDeltaCC) delta(stub shim.ChaincodeStubInterface, args []string, op shim.DeltaOp) pb.Response {
	if len(args) < 2 || len(args) > 3 {
		return shim.Error("usage: Add|Sub(key, n [,version])")
	}
	if reservedKey(args[0]) {
		return shim.Error("key uses the reserved schema prefix")
	}
	n, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || n < 0 {
		return shim.Error("n must be a non-negative int64")
	}
	ver := defaultVersion
	if len(args) == 3 {
		ver = args[2]
	}
	if err := shim.PutDelta(stub, args[0], typeID, op, int64BE(n), []byte(ver)); err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(nil)
}

func (t *TypedDeltaCC) transfer(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) < 3 || len(args) > 4 {
		return shim.Error("usage: Transfer(from, to, n [,version])")
	}
	if reservedKey(args[0]) || reservedKey(args[1]) {
		return shim.Error("key uses the reserved schema prefix")
	}
	n, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil || n < 0 {
		return shim.Error("n must be a non-negative int64")
	}
	ver := defaultVersion
	if len(args) == 4 {
		ver = args[3]
	}
	if err := shim.PutDelta(stub, args[0], typeID, shim.DeltaSub, int64BE(n), []byte(ver)); err != nil {
		return shim.Error(err.Error())
	}
	if err := shim.PutDelta(stub, args[1], typeID, shim.DeltaAdd, int64BE(n), []byte(ver)); err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(nil)
}

func (t *TypedDeltaCC) putPlain(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 {
		return shim.Error("usage: PutPlain(key, n)")
	}
	if reservedKey(args[0]) {
		return shim.Error("key uses the reserved schema prefix")
	}
	n, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return shim.Error("n must be an int64")
	}
	if err := stub.PutState(args[0], int64BE(n)); err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(nil)
}

func (t *TypedDeltaCC) revealDebit(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 {
		return shim.Error("usage: RevealDebit(key, n)")
	}
	if reservedKey(args[0]) {
		return shim.Error("key uses the reserved schema prefix")
	}
	n, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || n < 0 {
		return shim.Error("n must be a non-negative int64")
	}
	cur, err := stub.GetState(args[0])
	if err != nil {
		return shim.Error(err.Error())
	}
	if cur == nil {
		return shim.Error("key not found: " + args[0])
	}
	bal, err := decodeBE(cur)
	if err != nil {
		return shim.Error(err.Error())
	}
	if bal < n {
		return shim.Error("insufficient balance")
	}
	if err := stub.PutState(args[0], int64BE(bal-n)); err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(nil)
}

func (t *TypedDeltaCC) get(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 1 {
		return shim.Error("usage: Get(key)")
	}
	v, err := stub.GetState(args[0])
	if err != nil {
		return shim.Error(err.Error())
	}
	if v == nil {
		return shim.Error("key not found: " + args[0])
	}
	n, err := decodeBE(v)
	if err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success([]byte(strconv.FormatInt(n, 10)))
}

func main() {
	if err := shim.Start(new(TypedDeltaCC)); err != nil {
		fmt.Printf("Error starting TypedDeltaCC: %s\n", err)
	}
}
