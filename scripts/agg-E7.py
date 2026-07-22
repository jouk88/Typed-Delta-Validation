# agg-E7.py — aggregate E7 multi-hot-key breadth (N=10) with t-based 95% CI + figure.
# ours vs vanilla x keys {1,10,100} x rep 1..10. Metrics: valid_goodput_tps, mvcc conflict rate.
import json, os, glob, statistics, math
try:
    from scipy.stats import t as tdist
    def tcrit(df): return float(tdist.ppf(0.975, df))
except Exception:
    def tcrit(df): return {9:2.262}.get(df, 2.262)
D = os.path.join(os.path.dirname(__file__), "..", "data")
FIG = os.path.join(os.path.dirname(__file__), "..", "figures")
TBLS = os.path.join(D, "tables")

def ci(vals):
    n=len(vals); m=statistics.mean(vals); s=statistics.stdev(vals) if n>1 else 0.0
    return m, s, (tcrit(n-1)*s/math.sqrt(n) if n>1 else 0.0)

ccs=["ours","vanilla"]; keys=[1,10,100]
rows=[["condition","keys","n","mean_tps","ci_half","mean_mvcc_rate","ci_mvcc"]]
agg={}
for cc in ccs:
    for k in keys:
        tps=[]; mv=[]
        for f in sorted(glob.glob(os.path.join(D, "E7-multikey", f"E7-{cc}-k{k}-r*.json"))):
            d=json.load(open(f)); tps.append(d["valid_goodput_tps"])
            sub=d.get("submitted",1) or 1; mv.append(d.get("mvcc_invalid",0)/sub)
        m,s,h=ci(tps); mm,_,mh=ci(mv)
        agg[(cc,k)]=(m,h,mm,mh,len(tps))
        rows.append([cc,k,len(tps),f"{m:.2f}",f"{h:.2f}",f"{mm:.4f}",f"{mh:.4f}"])
# write CSVs
os.makedirs(TBLS, exist_ok=True)
for path in (os.path.join(D,"aggregates","agg-multikey.csv"), os.path.join(TBLS,"tbl-multikey.csv")):
    with open(path,"w") as w:
        for r in rows: w.write(",".join(map(str,r))+"\n")
print("=== E7 multi-hot-key breadth (N=10, mean tps ± 95% CI) ===")
for cc in ccs:
    print(cc+":", "  ".join(f"k{k}={agg[(cc,k)][0]:.1f}±{agg[(cc,k)][1]:.1f}" for k in keys))
# figure
try:
    import matplotlib; matplotlib.use("Agg"); import matplotlib.pyplot as plt
    os.makedirs(FIG, exist_ok=True)
    fig,ax=plt.subplots(figsize=(6,4))
    for cc,mk,col,lab in (("ours","s-","royalblue","ours (typed delta)"),
                          ("vanilla","o--","crimson","vanilla RMW")):
        ys=[agg[(cc,k)][0] for k in keys]; es=[agg[(cc,k)][1] for k in keys]
        ax.errorbar(keys, ys, yerr=es, fmt=mk, color=col, capsize=4, label=lab)
    ax.set_xscale("log"); ax.set_xticks(keys); ax.set_xticklabels(keys)
    ax.set_xlabel("number of hot keys (uniform)"); ax.set_ylabel("valid goodput (tps)")
    ax.set_title("Multi-hot-key goodput (uniform key distribution)"); ax.legend(); ax.grid(True,alpha=0.3)
    fig.tight_layout(); fp=os.path.join(FIG,"fig5_multikey_goodput.png"); fig.savefig(fp,dpi=160)
    print("wrote", fp)
except Exception as e:
    print("figure skipped:", e)
print("wrote data/aggregates/agg-multikey.csv + data/tables/tbl-multikey.csv")
