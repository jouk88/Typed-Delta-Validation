"""Fig.2 safety: prefix-invariant violation commits from the independent
block-replay analyzer, for the deterministic overdraft (E5) and the randomized
five-seed overdraft (E6). Reads data/tables/tbl-safety.csv.
Outputs figures/fig2_deterministic.png (a) and figures/fig2_randomized.png (b).
"""
import csv
import os

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

HERE = os.path.dirname(os.path.abspath(__file__))
TBL = os.path.normpath(os.path.join(HERE, "..", "data", "tables", "tbl-safety.csv"))
FIGS = os.path.normpath(os.path.join(HERE, "..", "figures"))
os.makedirs(FIGS, exist_ok=True)

e5 = {}          # condition -> violations
e6 = {}          # (condition, seed) -> violations
seeds = []
with open(TBL) as f:
    for r in csv.DictReader(f):
        v = int(r["independent_violations"])
        if r["experiment"] == "E5":
            e5[r["condition"]] = v
        else:
            s = int(r["seed"])
            if s not in seeds:
                seeds.append(s)
            e6[(r["condition"], s)] = v

CONDS = [
    ("ours", "royalblue", "ours (typed delta)"),
    ("nocheck", "seagreen", "no-check merge ablation"),
    ("ht", "darkorange", "append (high-throughput cc)"),
]

# --- (a) deterministic overdraft: one bar per condition ---
plt.figure(figsize=(6.0, 4.0))
xs = range(len(CONDS))
vals = [e5[c] for c, _, _ in CONDS]
cols = [col for _, col, _ in CONDS]
bars = plt.bar(xs, vals, color=cols, width=0.55)
for b, v in zip(bars, vals):
    plt.text(b.get_x() + b.get_width() / 2, v + 8, str(v),
             ha="center", va="bottom", fontsize=10)
plt.xticks(xs, ["ours\n(typed delta)", "no-check\nmerge ablation",
                "append\n(high-throughput cc)"], fontsize=9)
plt.ylabel("prefix-invariant violation commits")
plt.ylim(0, 500)
plt.title("(a) Deterministic overdraft (initial balance 100)")
plt.grid(True, axis="y", alpha=0.3)
plt.tight_layout()
plt.savefig(os.path.join(FIGS, "fig2_deterministic.png"), dpi=160)
plt.close()

# --- (b) randomized overdraft: grouped bars per seed ---
plt.figure(figsize=(6.0, 4.0))
w = 0.26
for i, (c, col, lab) in enumerate(CONDS):
    xs = [k + (i - 1) * w for k in range(len(seeds))]
    vals = [e6[(c, s)] for s in seeds]
    bars = plt.bar(xs, vals, width=w, color=col, label=lab)
    for b, v in zip(bars, vals):
        plt.text(b.get_x() + b.get_width() / 2, v + 8, str(v),
                 ha="center", va="bottom", fontsize=7)
plt.xticks(range(len(seeds)), [f"seed {s}" for s in seeds], fontsize=9)
plt.ylabel("prefix-invariant violation commits")
plt.ylim(0, 660)
plt.title("(b) Randomized overdraft across five seeds")
plt.legend(fontsize=7.5, ncol=3, loc="upper center")
plt.grid(True, axis="y", alpha=0.3)
plt.tight_layout()
plt.savefig(os.path.join(FIGS, "fig2_randomized.png"), dpi=160)
plt.close()

print("wrote figures/fig2_deterministic.png, figures/fig2_randomized.png")
print("E5:", e5)
for s in seeds:
    print(f"seed {s}:", {c: e6[(c, s)] for c, _, _ in CONDS})
