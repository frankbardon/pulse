# Z-score every column with its population SD (ddof 0) — the external
# materialization the standardize fixture is fitted to:
#   zscore.py data.csv > z.csv
import sys, pandas as pd
df = pd.read_csv(sys.argv[1])
((df - df.mean()) / df.std(ddof=0)).to_csv(sys.stdout, index=False, float_format="%.17g")
