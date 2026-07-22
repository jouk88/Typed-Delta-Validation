"""Fig.3 cost: per-block validation time vs deltas/block with 95% CI error bars
and overlaid linear regression (slope us/delta + R^2) for plain / NONNEG / RANGE.
Reads pre-aggregated data/aggregates/agg-cost.csv
  (variant,deltas_per_block,us_per_block_mean,us_per_block_ci_half,us_per_delta).
Outputs figures/fig3_cost.png. No scipy/numpy needed (plain least squares).
"""
import csv
import os
from collections import defaultdict

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

HERE = os.path.dirname(os.path.abspath(__file__))
DATA = os.path.normpath(os.path.join(HERE, "..", "data"))
FIGS = os.path.normpath(os.path.join(HERE, "..", "figures"))
os.makedirs(FIGS, exist_ok=True)

mean = defaultdict(dict)
ci = defaultdict(dict)
with open(os.path.join(DATA, "aggregates", "agg-cost.csv")) as f:
    for r in csv.DictReader(f):
        x = int(r["deltas_per_block"])
        mean[r["variant"]][x] = float(r["us_per_block_mean"])
        ci[r["variant"]][x] = float(r["us_per_block_ci_half"])


def linreg(xs, ys):
    n = len(xs)
    sx = sum(xs); sy = sum(ys)
    sxx = sum(x * x for x in xs); sxy = sum(x * y for x, y in zip(xs, ys))
    slope = (n * sxy - sx * sy) / (n * sxx - sx * sx)
    intercept = (sy - slope * sx) / n
    ybar = sy / n
    ss_tot = sum((y - ybar) ** 2 for y in ys)
    ss_res = sum((y - (slope * x + intercept)) ** 2 for x, y in zip(xs, ys))
    r2 = 1.0 - ss_res / ss_tot if ss_tot else 1.0
    return slope, intercept, r2


XS = [10, 50, 100, 500]
plt.figure(figsize=(6.0, 4.2))
series = [
    ("plain", "o", "gray", "plain writes"),
    ("NONNEG", "s", "royalblue", "typed delta, NONNEG"),
    ("RANGE", "^", "seagreen", "typed delta, full-range"),
]
for v, mk, col, lab in series:
    ys = [mean[v][x] for x in XS]
    es = [ci[v][x] for x in XS]
    slope, intercept, r2 = linreg(XS, ys)
    plt.errorbar(XS, ys, yerr=es, fmt=mk, color=col, capsize=3, linestyle="none",
                 label=f"{lab}")
    xline = [0, 520]
    plt.plot(xline, [slope * x + intercept for x in xline], "-", color=col, alpha=0.7,
             linewidth=1.3,
             label=f"  fit: {slope:.2f} µs/delta, R²={r2:.5f}")

plt.xlabel("writes per block")
plt.ylabel("block validation time (µs)")
plt.title("Commit-time validation cost")
plt.legend(fontsize=7.5)
plt.grid(True, alpha=0.3)
plt.tight_layout()
plt.savefig(os.path.join(FIGS, "fig3_cost.png"), dpi=160)
plt.close()

print("wrote figures/fig3_cost.png")
for v, _, _, _ in series:
    ys = [mean[v][x] for x in XS]
    slope, intercept, r2 = linreg(XS, ys)
    print(f"  {v:>6}: slope={slope:.4f} us/delta  intercept={intercept:.3f}  R^2={r2:.6f}")
