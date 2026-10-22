<!-- section question -->
You have one outcome — revenue, churn, a satisfaction score — and several candidate factors, and you want to know which of them explain it, how much each one moves the outcome when the others are held fixed, and how much of the outcome the factors explain together. This is regression: a numeric outcome takes a linear model, a yes/no outcome a logistic one.

It sounds like:

- "What explains a customer's revenue?"
- "Which factors matter most for churn?"
- "Does session time still matter once we account for past spending?"

Read three things from a fit: each coefficient (how much the outcome moves per unit of that factor, others held fixed), its interval and p-value (whether the data pins that movement down), and R-squared or the model's fit score (how much of the outcome the factors explain together).

<!-- section pitfalls -->
- **A driver in the model is not a cause in the world.** A coefficient says how the outcome moves with a factor in this data; it is not what would happen if you changed that factor, unless the data come from an experiment that set it.
- **Correlated factors share the credit.** When two predictors move together, the model cannot tell them apart: coefficients swing, intervals widen and a factor can look unimportant only because its twin is in the model; check collinearity before ranking factors.
- **Coefficients in different units are not comparable.** A larger coefficient is not a stronger driver when one factor is in dollars and another in minutes; compare standardized figures or the change across a typical range.
- **A high R-squared can be trivial.** An outcome explained by its own earlier value (last month's revenue predicting this month's) fits well and says little; ask what the model adds beyond the obvious.
- **Rows that share conditions are not independent.** Customers in the same region, or repeated rows per person, make standard errors too small; use a clustered or robust covariance, and expect wider intervals when there are only a few clusters.
- **Too many factors for the rows overfits.** With few rows per predictor the fit chases noise; prefer fewer factors, a penalized fit or selection, and score the model on held-out rows.
- **A missing value drops the whole row.** Every predictor's nulls remove the row from the fit, so the model can quietly run on far fewer rows than the cohort; check the number of observations it reports.
