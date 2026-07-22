"""Shared t-based 95% CI helper for the aggregation scripts (no scipy dependency).
CI_half = t(N-1, 0.975) * stdev / sqrt(N). Falls back to a t-table when scipy absent."""
import math
import statistics

# t(0.975, df) table for the df values used here (df = N-1).
_T_TABLE = {
    1: 12.706, 2: 4.303, 3: 3.182, 4: 2.776, 5: 2.571, 6: 2.447, 7: 2.365,
    8: 2.306, 9: 2.262, 10: 2.228, 11: 2.201, 12: 2.179, 13: 2.160, 14: 2.145,
    15: 2.131, 16: 2.120, 17: 2.110, 18: 2.101, 19: 2.093, 20: 2.086,
}

try:
    from scipy.stats import t as _scipy_t

    def t_crit(df):
        return float(_scipy_t.ppf(0.975, df))
except Exception:  # scipy not installed -> table lookup
    def t_crit(df):
        if df in _T_TABLE:
            return _T_TABLE[df]
        # nearest available df (conservative for gaps); large df -> z
        if df > 20:
            return 1.96 + (2.086 - 1.96) * (30 - min(df, 30)) / 10.0
        keys = sorted(_T_TABLE)
        nearest = min(keys, key=lambda k: abs(k - df))
        return _T_TABLE[nearest]


def mean_ci(vals):
    """Return (mean, ci_half, n) for a list of numbers using t-based 95% CI."""
    n = len(vals)
    mu = statistics.mean(vals)
    if n < 2:
        return mu, 0.0, n
    sd = statistics.stdev(vals)
    ci = t_crit(n - 1) * sd / math.sqrt(n)
    return mu, ci, n
