"""E4 reveal sweep aggregation: valid goodput vs reveal ratio.
Reads E4-rr{0,10,50,100}-r{NN}.json (N=20/10/10/20), cc=ours, conc=50, count=500.
Groups by reveal_ratio, mean valid_goodput_tps +/- t-based 95% CI (variable N).
Outputs:
  data/aggregates/agg-reveal.csv  (reveal_ratio,mean_tps,ci_half,n)
  figures/fig4_reveal.png
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

RATIOS = [0, 10, 50, 100]
goodput = defaultdict(list)
for rr in RATIOS:
    for path in sorted(glob.glob(os.path.join(DATA, "E4-reveal", f"E4-rr{rr}-r*.json"))):
        d = json.load(open(path))
        goodput[rr].append(d["valid_goodput_tps"])

agg = {}
with open(os.path.join(AGG, "agg-reveal.csv"), "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["reveal_ratio", "mean_tps", "ci_half", "n"])
    for rr in RATIOS:
        mu, ci, n = mean_ci(goodput[rr])
        agg[rr] = (mu, ci, n)
        w.writerow([rr, f"{mu:.2f}", f"{ci:.2f}", n])

plt.figure(figsize=(6.0, 4.0))
mus = [agg[rr][0] for rr in RATIOS]
cis = [agg[rr][1] for rr in RATIOS]
plt.errorbar(RATIOS, mus, yerr=cis, fmt="s-", color="royalblue", capsize=4,
             label="ours (typed delta), conc 50")
plt.xlabel("reveal ratio (% read-modify-write fallback)")
plt.ylabel("valid goodput (tps)")
plt.title("Goodput vs. reveal ratio")
plt.ylim(bottom=0)
plt.legend(fontsize=8)
plt.grid(True, alpha=0.3)
plt.tight_layout()
plt.savefig(os.path.join(FIGS, "fig4_reveal.png"), dpi=160)
plt.close()

print("wrote data/aggregates/agg-reveal.csv, figures/fig4_reveal.png")
for rr in RATIOS:
    mu, ci, n = agg[rr]
    print(f"  rr{rr:>3}%: {mu:8.2f} +/- {ci:7.2f}  (n={n})")
