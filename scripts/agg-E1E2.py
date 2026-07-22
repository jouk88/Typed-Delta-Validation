"""E1+E2 aggregation: valid goodput vs concurrency for 4 conditions.
Reads E1-{ours,nocheck,vanilla}-c{C}-r{1..10}.json and E2-ht-c{C}-r{1..10}.json (ht -> "append").
Groups by (condition, concurrency), computes mean valid_goodput_tps +/- t-based 95% CI over 10 reps.
Outputs:
  data/aggregates/agg-goodput.csv  (condition,concurrency,mean_tps,ci_half,n)
  data/aggregates/agg-mvcc.csv     (concurrency,mvcc_conflict_rate_mean,ci_half,n)  vanilla mvcc_invalid/submitted
  figures/fig1_goodput.png, figures/fig1_mvcc.png
"""
import csv
import glob
import json
import os
from collections import defaultdict

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

from _tstat import mean_ci

HERE = os.path.dirname(os.path.abspath(__file__))
DATA = os.path.normpath(os.path.join(HERE, "..", "data"))
AGG = os.path.join(DATA, "aggregates")
FIGS = os.path.normpath(os.path.join(HERE, "..", "figures"))
os.makedirs(FIGS, exist_ok=True)

CONCS = [1, 5, 10, 25, 50, 100]
# condition -> (file glob prefix)
E1_CONDS = ["ours", "nocheck", "vanilla"]

# goodput samples: (condition, conc) -> [tps...]
goodput = defaultdict(list)
# vanilla mvcc conflict rate: conc -> [mvcc_invalid/submitted...]
mvcc_rate = defaultdict(list)


def load(path):
    with open(path) as f:
        return json.load(f)


for cond in E1_CONDS:
    for c in CONCS:
        for path in sorted(glob.glob(os.path.join(DATA, "E1-throughput", f"E1-{cond}-c{c}-r*.json"))):
            d = load(path)
            goodput[(cond, c)].append(d["valid_goodput_tps"])
            if cond == "vanilla":
                sub = d["submitted"]
                mvcc_rate[c].append(d["mvcc_invalid"] / sub if sub else 0.0)

# E2 ht -> append
for c in CONCS:
    for path in sorted(glob.glob(os.path.join(DATA, "E2-append", f"E2-ht-c{c}-r*.json"))):
        goodput[("append", c)].append(load(path)["valid_goodput_tps"])

CONDS = ["ours", "nocheck", "vanilla", "append"]

# --- write agg-goodput.csv ---
agg = {}
with open(os.path.join(AGG, "agg-goodput.csv"), "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["condition", "concurrency", "mean_tps", "ci_half", "n"])
    for cond in CONDS:
        for c in CONCS:
            mu, ci, n = mean_ci(goodput[(cond, c)])
            agg[(cond, c)] = (mu, ci, n)
            w.writerow([cond, c, f"{mu:.2f}", f"{ci:.2f}", n])

# --- write agg-mvcc.csv ---
mvcc_agg = {}
with open(os.path.join(AGG, "agg-mvcc.csv"), "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["concurrency", "mvcc_conflict_rate_mean", "ci_half", "n"])
    for c in CONCS:
        mu, ci, n = mean_ci(mvcc_rate[c])
        mvcc_agg[c] = (mu, ci, n)
        w.writerow([c, f"{mu:.4f}", f"{ci:.4f}", n])

# --- Fig.1 goodput ---
styles = {
    "ours": ("s-", "royalblue", "ours (typed delta)"),
    "nocheck": ("^-", "seagreen", "no-check merge ablation"),
    "vanilla": ("o-", "crimson", "vanilla RMW"),
    "append": ("D-", "darkorange", "append (high-throughput cc)"),
}
plt.figure(figsize=(6.0, 4.0))
for cond in CONDS:
    mus = [agg[(cond, c)][0] for c in CONCS]
    cis = [agg[(cond, c)][1] for c in CONCS]
    fmt, col, lab = styles[cond]
    plt.errorbar(CONCS, mus, yerr=cis, fmt=fmt, color=col, label=lab, capsize=3)
plt.yscale("log")
plt.xlabel("concurrency (offered load)")
plt.ylabel("valid goodput (tps, log scale)")
plt.title("(a) Goodput vs. concurrency")
plt.legend(fontsize=8)
plt.grid(True, which="both", alpha=0.3)
plt.tight_layout()
plt.savefig(os.path.join(FIGS, "fig1_goodput.png"), dpi=160)
plt.close()

# --- Fig.1 mvcc (vanilla conflict rate) ---
plt.figure(figsize=(6.0, 4.0))
mus = [mvcc_agg[c][0] for c in CONCS]
cis = [mvcc_agg[c][1] for c in CONCS]
plt.errorbar(CONCS, mus, yerr=cis, fmt="o-", color="crimson", capsize=3,
             label="vanilla MVCC_READ_CONFLICT rate")
plt.xlabel("concurrency (offered load)")
plt.ylabel("MVCC conflict rate (mvcc_invalid / submitted)")
plt.title("(b) Vanilla MVCC conflict rate")
plt.ylim(-0.02, 1.02)
plt.legend(fontsize=8)
plt.grid(True, alpha=0.3)
plt.tight_layout()
plt.savefig(os.path.join(FIGS, "fig1_mvcc.png"), dpi=160)
plt.close()

print("wrote data/aggregates/agg-goodput.csv, data/aggregates/agg-mvcc.csv")
print("wrote figures/fig1_goodput.png, figures/fig1_mvcc.png")
print("\n-- goodput mean +/- CI (n) --")
for cond in CONDS:
    print(cond)
    for c in CONCS:
        mu, ci, n = agg[(cond, c)]
        print(f"  c{c:>3}: {mu:8.2f} +/- {ci:7.2f}  (n={n})")
print("\n-- vanilla MVCC conflict rate --")
for c in CONCS:
    mu, ci, n = mvcc_agg[c]
    print(f"  c{c:>3}: {mu:.4f} +/- {ci:.4f} (n={n})")
