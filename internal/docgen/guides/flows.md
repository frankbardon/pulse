<!-- section question -->
You have rows that record a move from one state to another — a customer's plan last month and this month, a member's tier before and after, a voter's choice in two waves — and you want to know who stays, who switches, where the switchers go, and what the mix will look like if the moves continue.

It sounds like:

- "Where do customers move between plans?"
- "Who switched from silver to gold?"
- "What share of each tier did we keep?"

Build a from-by-to crosstab of counts: each row is a starting state, each column an ending state, so a row read across shows where that state's members went. A retention overlay turns each row into stay, switch and churn rates; a Markov overlay turns the table into the chance of each move (transition probabilities) and projects the mix forward; a symmetry overlay tests whether moves between two states balance out. When the data is one row per snapshot rather than one per move, a previous-state attribute adds each row's earlier state first.

<!-- section pitfalls -->
- **Read along the row, not down the column.** A row shows where one starting state's members went; a column shows where an ending state's members came from. They answer different questions.
- **Name the states nobody leaves.** A state such as churned has moves in but none out; mark it absorbing so it is counted as churn and not reported as a starting state.
- **The first snapshot has no move.** In snapshot data each customer's first row has no earlier state and reaches no cell; the table counts moves, not customers.
- **A projection assumes the moves stay the same.** A Markov projection repeats the observed rates step after step; it is a what-if under steady conditions, not a forecast.
- **Small rows give unsteady rates.** A starting state with few members can show a striking stay or churn rate by luck; keep the row counts beside the rates.
- **Balanced moves are not no movement.** A symmetry test that does not reject says moves between two states roughly cancel out; plenty of members may still have switched.
