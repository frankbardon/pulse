# Run 2026-11-27-r4: broken harness run (not a measurement)

r4 was a confirmation rerun after the composition key change. Its agents started with about 584K tokens of context, against about 187K in r2 and r3. They wrote terse reports and mostly skipped the examples. One (`segment_revenue_bands`) was killed by the harness ("Autocompact is thrashing"). The simulate agent read three unscoped payloads that overflowed its output and gave no spec. The import task was not run. Score: 17/20 (`score.txt`).

The owner ruled (2026-11-27) that r3 is the green run. r4 is kept only as a record of the broken harness.
