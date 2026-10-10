# Glossary

Plain-language definitions of the terms the guidance relies on, sorted by ID. Catalog entries link here for every term they cite.

<a id="term-adjusted-r-squared"></a>

## adjusted-r-squared

R-squared corrected for the number of predictors: it rises only when an added predictor's t value exceeds 1 in size, which a useless predictor still does about one time in three.

**Why care:** Use it to compare models with different numbers of predictors on the same rows. It can fall below zero when the predictors fit worse than chance would predict.

**Forms:** adjusted r-squared, adjusted r squared, adjusted r², adj_r2

**See also:** [`r-squared`](#term-r-squared), [`overfitting`](#term-overfitting)

<a id="term-alpha"></a>

## alpha

The p-value threshold you pick before looking at the data, below which you call a result significant.

**Why care:** It is the false-alarm rate you accept when there is truly no effect: the share of such tests you would wrongly call significant. Pick it up front; moving it after seeing results defeats its purpose.

**Forms:** alpha, significance level

**See also:** [`p-value`](#term-p-value), [`statistical-significance`](#term-statistical-significance), [`multiple-comparisons`](#term-multiple-comparisons)

<a id="term-baseline"></a>

## baseline

The reference a figure is compared with: a chosen period, a named group, a population, a total or another request's result.

**Why care:** Every comparison is only as meaningful as its baseline. Say which one you used, and pick one that is stable and large enough to compare against.

**See also:** [`index-value`](#term-index-value), [`margin`](#term-margin)

<a id="term-centroid"></a>

## centroid

The centre point of a cluster: the average of every field across the rows in it.

**Why care:** Comparing centroids is how you describe what makes one segment different from another.

**Forms:** centroid, centroids

**See also:** [`distance`](#term-distance), [`similarity`](#term-similarity)

<a id="term-chi-square"></a>

## chi-square

A test statistic that adds up how far observed counts in each cell sit from the counts you would expect if nothing were going on.

**Why care:** It measures the evidence that two categorical fields are related, but not how strongly; pair it with Cramer's V or phi for the size.

**Forms:** chi-square, chi-squared, chi square, chi squared

**See also:** [`test-statistic`](#term-test-statistic), [`independence`](#term-independence), [`cramers-v`](#term-cramers-v), [`cross-tabulation`](#term-cross-tabulation)

<a id="term-cohens-d"></a>

## cohens-d

The difference between two means expressed in standard deviations.

**Why care:** It makes differences comparable across measures with different units: a gap of half a standard deviation is the same standardized size on any scale, though whether it matters depends on the measure.

**Forms:** cohen's d, cohens d, cohens_d

**See also:** [`effect-size`](#term-effect-size), [`standard-deviation`](#term-standard-deviation), [`t-statistic`](#term-t-statistic)

<a id="term-cohens-h"></a>

## cohens-h

The difference between two proportions, put on a scale that treats a change near 0% or 100% as bigger than the same change near 50%.

**Why care:** Comparing raw percentage-point gaps can mislead near the edges; this puts proportion differences on an even footing.

**Forms:** cohen's h, cohens h, cohens_h

**See also:** [`effect-size`](#term-effect-size), [`cohens-d`](#term-cohens-d)

<a id="term-confidence-interval"></a>

## confidence-interval

A range built by a method that, over repeated samples, captures the true value a stated share of the time (such as 95%); any single interval either contains it or not.

**Why care:** It shows the uncertainty in plain units. A wide interval means you know less than the single estimate suggests.

**Forms:** confidence interval, confidence intervals

**See also:** [`standard-error`](#term-standard-error), [`p-value`](#term-p-value), [`sample-size`](#term-sample-size)

<a id="term-continuity-correction"></a>

## continuity-correction

A half-step adjustment used when a smooth curve approximates a statistic that moves in whole steps, such as a count or a rank sum.

**Why care:** It keeps approximate p-values from coming out too small in small samples. Tools differ on whether they apply it, which explains small mismatches between them.

**Forms:** continuity correction, yates correction, yates' correction

**See also:** [`p-value`](#term-p-value), [`exact-test`](#term-exact-test)

<a id="term-correlation"></a>

## correlation

How closely two measures move together in a straight line, from -1 (opposite) through 0 (no straight-line link) to 1 (in lockstep).

**Why care:** It shows association, not cause. It also misses curved relationships and can be distorted by a few outliers.

**Forms:** correlation, correlations, correlation coefficient

**See also:** [`covariance`](#term-covariance), [`r-squared`](#term-r-squared), [`outlier`](#term-outlier), [`independence`](#term-independence)

<a id="term-covariance"></a>

## covariance

How two numeric fields vary together: positive when they rise together, negative when one rises as the other falls.

**Why care:** Its size depends on the units, so it is hard to read on its own; correlation is the unit-free version.

**Forms:** covariance, covariances

**See also:** [`correlation`](#term-correlation), [`variance`](#term-variance)

<a id="term-cramers-v"></a>

## cramers-v

The strength of the relationship between two categorical fields, from 0 (unrelated) to 1 (one fully determines the other).

**Why care:** A chi-square p-value weighs the evidence that the fields are related; Cramer's V says how strongly, and unlike chi-square it does not grow with the number of rows. Compare V only between tables of the same shape.

**Forms:** cramer's v, cramers v, cramers_v

**See also:** [`chi-square`](#term-chi-square), [`phi`](#term-phi), [`effect-size`](#term-effect-size), [`cross-tabulation`](#term-cross-tabulation)

<a id="term-credible-interval"></a>

## credible-interval

A Bayesian range that holds a parameter with a stated probability, given the prior, the model and the data.

**Why care:** Unlike a confidence interval it is a direct probability statement about the parameter, but that probability is conditional on the prior and the model; change either and the interval changes.

**Forms:** credible interval, credible intervals

**See also:** [`posterior`](#term-posterior), [`prior`](#term-prior), [`confidence-interval`](#term-confidence-interval)

<a id="term-cross-tabulation"></a>

## cross-tabulation

A table that counts rows for every combination of two categorical fields, one along the rows and one along the columns.

**Why care:** It is the simplest way to see whether two categories go together; a chi-square test then says how surprising the pattern would be if the two fields were unrelated.

**Forms:** cross-tabulation, contingency table, contingency tables

**See also:** [`chi-square`](#term-chi-square), [`cramers-v`](#term-cramers-v)

<a id="term-degrees-of-freedom"></a>

## degrees-of-freedom

How many independent pieces of information a statistic was built from, usually the row or group count minus the things estimated along the way.

**Why care:** Tests need it to turn a statistic into a p-value. Very few degrees of freedom means the result rests on thin evidence.

**Forms:** degrees of freedom, degree of freedom

**See also:** [`test-statistic`](#term-test-statistic), [`sample-size`](#term-sample-size)

<a id="term-deviance"></a>

## deviance

A measure of how far a fitted model's predictions sit from the observed outcomes, on a log-likelihood scale; lower means a closer fit.

**Why care:** On its own the number has no fixed scale. Compare it with the null deviance (the intercept-only model) or with another model fitted to the same rows and outcome.

**Forms:** deviance, deviances, null deviance

**See also:** [`generalized-linear-model`](#term-generalized-linear-model), [`pseudo-r-squared`](#term-pseudo-r-squared)

<a id="term-distance"></a>

## distance

A number for how far apart two rows are across several fields; zero means identical.

**Why care:** Clustering groups rows by distance, so fields with large units dominate unless you standardise them first.

**See also:** [`similarity`](#term-similarity), [`centroid`](#term-centroid), [`z-score`](#term-z-score)

<a id="term-effect-size"></a>

## effect-size

A number that says how BIG a difference or relationship is, on a scale that does not grow just because you have more rows.

**Why care:** It says how big the difference is, while the p-value says how surprising the data would be if there were no difference. Whether that size matters depends on your context. Report both.

**Forms:** effect size, effect sizes

**See also:** [`p-value`](#term-p-value), [`statistical-significance`](#term-statistical-significance), [`cohens-d`](#term-cohens-d), [`eta-squared`](#term-eta-squared), [`cramers-v`](#term-cramers-v)

<a id="term-effective-sample-size"></a>

## effective-sample-size

The number of unweighted rows that would give the same precision as your weighted sample.

**Why care:** Uneven weights cost precision: a weighted sample of 1,000 can carry the information of far fewer rows.

**Forms:** effective sample size, effective n

**See also:** [`weighting`](#term-weighting), [`sample-size`](#term-sample-size), [`standard-error`](#term-standard-error)

<a id="term-eigenvalue"></a>

## eigenvalue

How much of the total variation in a set of fields one underlying dimension accounts for.

**Why care:** It ranks the dimensions a principal component analysis finds; dimensions with small eigenvalues explain little and are usually dropped.

**Forms:** eigenvalue, eigenvalues

**See also:** [`principal-component`](#term-principal-component), [`variance`](#term-variance)

<a id="term-epsilon-squared"></a>

## epsilon-squared

The rank-based counterpart of eta-squared, used with Kruskal-Wallis: how much of the ordering of rows is explained by group.

**Why care:** It gives a size to a non-parametric group comparison, which otherwise only yields a p-value.

**Forms:** epsilon squared, epsilon-squared, epsilon_squared

**See also:** [`eta-squared`](#term-eta-squared), [`non-parametric`](#term-non-parametric), [`rank`](#term-rank)

<a id="term-eta-squared"></a>

## eta-squared

The share of the total variation in an outcome that is explained by which group a row belongs to.

**Why care:** It turns an ANOVA into "how much does group membership matter". It runs a little high in small samples; omega-squared corrects for that.

**Forms:** eta squared, eta-squared, eta_squared

**See also:** [`effect-size`](#term-effect-size), [`omega-squared`](#term-omega-squared), [`partial-eta-squared`](#term-partial-eta-squared), [`f-statistic`](#term-f-statistic)

<a id="term-exact-test"></a>

## exact-test

A test whose p-value is computed exactly from every possible arrangement of the data rather than from a large-sample approximation.

**Why care:** It stays valid with tiny counts where approximations such as chi-square break down. The cost is computation, so it suits small tables.

**Forms:** exact test, exact tests

**See also:** [`p-value`](#term-p-value), [`chi-square`](#term-chi-square), [`continuity-correction`](#term-continuity-correction)

<a id="term-f-statistic"></a>

## f-statistic

The test statistic of an ANOVA: how much group means spread apart compared with how much rows spread within groups.

**Why care:** Values near 1 mean the groups differ about as much as noise would make them; larger values are stronger evidence against all group means being equal. Read the p-value to judge how large is large, and an effect size for how big the difference is.

**Forms:** f-statistic, f statistic, f-ratio

**See also:** [`test-statistic`](#term-test-statistic), [`variance`](#term-variance), [`eta-squared`](#term-eta-squared), [`post-hoc-test`](#term-post-hoc-test)

<a id="term-factor"></a>

## factor

An unobserved quality, such as satisfaction, that several measured fields are assumed to reflect together.

**Why care:** Grouping questions into factors lets you measure something no single question captures well.

**Forms:** latent factor, latent factors, factor analysis

**See also:** [`loading`](#term-loading), [`principal-component`](#term-principal-component), [`reliability`](#term-reliability)

<a id="term-false-discovery-rate"></a>

## false-discovery-rate

The expected share of false positives among the results you call significant.

**Why care:** Holding it at alpha (Benjamini-Hochberg, Benjamini-Yekutieli) keeps more power than family-wise control when many tests run, but accepts that a few of the hits may be false.

**Forms:** false discovery rate, fdr

**See also:** [`multiple-comparisons`](#term-multiple-comparisons), [`family-wise-error`](#term-family-wise-error), [`alpha`](#term-alpha)

<a id="term-family-wise-error"></a>

## family-wise-error

The chance that a group of tests produces at least one false positive, counted over the whole group rather than test by test.

**Why care:** Holding it at alpha (Bonferroni, Holm) makes any single significant hit trustworthy, at the cost of missing real but modest effects when the group is large.

**Forms:** family-wise error, family-wise error rate, familywise error, fwer

**See also:** [`multiple-comparisons`](#term-multiple-comparisons), [`false-discovery-rate`](#term-false-discovery-rate), [`alpha`](#term-alpha)

<a id="term-generalized-linear-model"></a>

## generalized-linear-model

A regression for outcomes that are not plain numbers on a line, such as yes/no or counts: a link function ties the predictors to the outcome's expected value.

**Why care:** It keeps predictions in range (probabilities between zero and one, counts above zero), but its coefficients are on the link scale, so read them through the link before talking about sizes.

**Forms:** generalized linear model, generalized linear models, glm, glms

**See also:** [`link-function`](#term-link-function), [`logistic-regression`](#term-logistic-regression), [`deviance`](#term-deviance), [`regression-coefficient`](#term-regression-coefficient)

<a id="term-goodness-of-fit"></a>

## goodness-of-fit

How well observed data match a stated distribution or a set of expected counts.

**Why care:** A goodness-of-fit test can only flag a mismatch. A large p-value does not show the data follow the distribution, and with few rows real departures go undetected.

**Forms:** goodness of fit, goodness-of-fit

**See also:** [`normal-distribution`](#term-normal-distribution), [`chi-square`](#term-chi-square), [`statistical-power`](#term-statistical-power)

<a id="term-heteroscedasticity"></a>

## heteroscedasticity

Residuals whose spread changes across the data, for example growing as the predicted value grows, instead of staying constant.

**Why care:** Classic regression standard errors assume a constant spread; when it changes, they and the p-values built on them can be off in either direction, even though the coefficients themselves stay unbiased.

**Forms:** heteroscedasticity, heteroskedasticity, heteroscedastic, non-constant variance

**See also:** [`residual`](#term-residual), [`standard-error`](#term-standard-error), [`homogeneity-of-variance`](#term-homogeneity-of-variance)

<a id="term-homogeneity-of-variance"></a>

## homogeneity-of-variance

The assumption that every group has roughly the same spread (variance) around its own mean.

**Why care:** Classic ANOVA and Tukey's test rely on it; when spreads differ, especially with unequal group sizes, their p-values drift. Welch's versions do not need it.

**Forms:** homogeneity of variance, equal variances, equal variance, homoscedasticity

**See also:** [`variance`](#term-variance), [`f-statistic`](#term-f-statistic), [`sphericity`](#term-sphericity)

<a id="term-independence"></a>

## independence

Two things are independent when knowing one tells you nothing about the other; rows are independent when no row influences another.

**Why care:** Most tests assume independent rows. Repeated answers from the same person break it, and need a paired or repeated-measures test instead.

**See also:** [`chi-square`](#term-chi-square), [`correlation`](#term-correlation), [`paired-data`](#term-paired-data), [`repeated-measures`](#term-repeated-measures)

<a id="term-index-value"></a>

## index-value

A ratio to a baseline scaled so the baseline is 100: 120 is 20% above it, 80 is 20% below it.

**Why care:** It puts groups of very different sizes on one scale, but it hides the absolute gap and swings wildly when the baseline is small.

**Forms:** index value, index values

**See also:** [`baseline`](#term-baseline), [`percentage-point`](#term-percentage-point)

<a id="term-kendall-tau"></a>

## kendall-tau

A rank correlation from -1 to 1 based on how many pairs of rows are in the same order on both fields versus the opposite order.

**Why care:** It copes well with small samples and many ties. It usually comes out smaller than Spearman's rho on the same data, so do not compare the two directly.

**Forms:** kendall's tau, kendall tau, tau-b, tau_b

**See also:** [`correlation`](#term-correlation), [`spearman-rho`](#term-spearman-rho), [`rank`](#term-rank), [`ties`](#term-ties)

<a id="term-kurtosis"></a>

## kurtosis

How heavy a distribution's tails are compared with the normal distribution: how prone it is to extreme values.

**Why care:** Heavy tails mean outliers turn up more often than a bell curve would predict, which can mislead tests that assume normality.

**Forms:** kurtosis

**See also:** [`skew`](#term-skew), [`normal-distribution`](#term-normal-distribution), [`outlier`](#term-outlier)

<a id="term-link-function"></a>

## link-function

The transformation a generalized linear model applies to the outcome's expected value before relating it to the predictors, such as the log or the log-odds.

**Why care:** A coefficient moves the outcome on the link scale, not in the outcome's own units: under a log link it multiplies the expected value, under the logit it multiplies the odds.

**Forms:** link function, link functions

**See also:** [`generalized-linear-model`](#term-generalized-linear-model), [`logistic-regression`](#term-logistic-regression), [`odds-ratio`](#term-odds-ratio)

<a id="term-listwise-deletion"></a>

## listwise-deletion

Dropping a whole row from an analysis when any of the fields it uses is missing.

**Why care:** Every figure then comes from the same rows, which keeps them consistent, but you can lose many rows when several fields each have gaps.

**Forms:** listwise deletion

**See also:** [`pairwise-deletion`](#term-pairwise-deletion), [`missing-value`](#term-missing-value)

<a id="term-loading"></a>

## loading

How strongly one original field is tied to a component or factor.

**Why care:** Loadings are how you name a component: the fields with the largest loadings tell you what it represents.

**Forms:** factor loading, factor loadings, component loading, component loadings

**See also:** [`principal-component`](#term-principal-component), [`factor`](#term-factor)

<a id="term-logistic-regression"></a>

## logistic-regression

A regression for a yes/no outcome that models the log-odds of yes as a straight-line function of the predictors.

**Why care:** Each coefficient is a change in log-odds; raised to the power e it becomes an odds ratio. Odds are not probabilities, so a constant odds ratio means different probability shifts at different starting points.

**Forms:** logistic regression, logistic regressions, logit, log-odds

**See also:** [`generalized-linear-model`](#term-generalized-linear-model), [`odds-ratio`](#term-odds-ratio), [`link-function`](#term-link-function)

<a id="term-margin"></a>

## margin

In a cross-tabulation, the figure for a whole row, a whole column or the whole table (the grand total), shown along its edges.

**Why care:** Cells are often read against their margin. The margin is whatever the cell aggregator gives for the full row or column: a total for counts and sums, an overall average for means.

**Forms:** margin, margins, marginal total, marginal totals

**See also:** [`cross-tabulation`](#term-cross-tabulation), [`baseline`](#term-baseline)

<a id="term-mean"></a>

## mean

The average: add the values up and divide by how many there are.

**Why care:** It is the usual "typical value", but a few extreme values can drag it far from where most rows sit; compare it with the median.

**See also:** [`median`](#term-median), [`outlier`](#term-outlier), [`skew`](#term-skew)

<a id="term-median"></a>

## median

The middle value once the values are sorted: half the rows sit below it, half above.

**Why care:** It barely moves when a few extreme values appear, so it is a safer "typical value" for skewed things like income or response time.

**See also:** [`mean`](#term-mean), [`percentile`](#term-percentile), [`skew`](#term-skew)

<a id="term-missing-value"></a>

## missing-value

A field with no recorded value for a row (a null).

**Why care:** How missing values are handled changes results: dropping them can bias a sample if they are not missing at random.

**See also:** [`listwise-deletion`](#term-listwise-deletion), [`pairwise-deletion`](#term-pairwise-deletion)

<a id="term-monotonic-trend"></a>

## monotonic-trend

A pattern that keeps going one way, always rising or always falling, though not necessarily in a straight line.

**Why care:** Rank-based measures such as Spearman's rho, Kendall's tau and the Mann-Kendall trend test detect this kind of pattern; they can miss a link that rises and then falls.

**Forms:** monotonic, monotone, monotonic trend

**See also:** [`spearman-rho`](#term-spearman-rho), [`kendall-tau`](#term-kendall-tau), [`correlation`](#term-correlation)

<a id="term-multicollinearity"></a>

## multicollinearity

Predictors in a model that are strongly related to each other.

**Why care:** The model can still predict well, but it cannot tell the overlapping predictors' effects apart, so their coefficients become unstable and hard to trust.

**Forms:** multicollinearity, collinearity, collinear

**See also:** [`regression-coefficient`](#term-regression-coefficient), [`correlation`](#term-correlation)

<a id="term-multiple-comparisons"></a>

## multiple-comparisons

Running many tests at once, which raises the chance that at least one comes out significant by luck alone.

**Why care:** Twenty tests at the usual alpha will on average flag one false positive even when nothing is going on. Adjust for it, or treat a lone hit with suspicion.

**Forms:** multiple comparisons, multiple testing

**See also:** [`p-value`](#term-p-value), [`alpha`](#term-alpha), [`post-hoc-test`](#term-post-hoc-test), [`family-wise-error`](#term-family-wise-error), [`false-discovery-rate`](#term-false-discovery-rate)

<a id="term-non-parametric"></a>

## non-parametric

A method that works on ranks or counts instead of assuming the data follow a particular shape such as the normal distribution.

**Why care:** Reach for one when data are skewed, have outliers or are ordinal ratings. It is robust, at the cost of some power when the data really are normal.

**Forms:** non-parametric, nonparametric, distribution-free

**See also:** [`rank`](#term-rank), [`normal-distribution`](#term-normal-distribution), [`outlier`](#term-outlier)

<a id="term-normal-distribution"></a>

## normal-distribution

The symmetric bell-shaped pattern many measurements roughly follow, with most values near the mean and fewer further out.

**Why care:** Many tests assume it. Strong skew or heavy tails make those tests less trustworthy; a non-parametric test is the usual fallback.

**Forms:** normal distribution, normally distributed, bell curve

**See also:** [`skew`](#term-skew), [`kurtosis`](#term-kurtosis), [`non-parametric`](#term-non-parametric), [`z-score`](#term-z-score)

<a id="term-null-hypothesis"></a>

## null-hypothesis

The boring default a test assumes until the data argue otherwise, usually "there is no difference" or "there is no relationship".

**Why care:** A test only measures how surprising your data would be if the null were true. Failing to reject it is not proof that it is true.

**Forms:** null hypothesis, null hypotheses

**See also:** [`p-value`](#term-p-value), [`statistical-significance`](#term-statistical-significance), [`test-statistic`](#term-test-statistic)

<a id="term-odds-ratio"></a>

## odds-ratio

How many times higher the odds of an outcome are in one group than in another; 1 means no difference.

**Why care:** Logistic models report effects this way. Odds are not probabilities, so an odds ratio of 2 does not mean "twice as likely" unless the outcome is rare.

**Forms:** odds ratio, odds ratios

**See also:** [`regression-coefficient`](#term-regression-coefficient), [`effect-size`](#term-effect-size)

<a id="term-omega-squared"></a>

## omega-squared

An estimate of the share of variation explained by groups, adjusted so it does not overstate the effect the way eta-squared does in small samples.

**Why care:** Prefer it when groups are small; it is the less optimistic, more honest version of eta-squared.

**Forms:** omega squared, omega-squared, omega_squared

**See also:** [`eta-squared`](#term-eta-squared), [`effect-size`](#term-effect-size)

<a id="term-outlier"></a>

## outlier

A value far away from the rest of the data.

**Why care:** Outliers can be errors or the most interesting rows. Either way they can pull means and correlations badly, so look before you drop them.

**See also:** [`z-score`](#term-z-score), [`percentile`](#term-percentile), [`median`](#term-median), [`non-parametric`](#term-non-parametric)

<a id="term-overdispersion"></a>

## overdispersion

Count data that vary more than a Poisson model allows, which assumes the variance equals the mean.

**Why care:** It is common in real counts. A model that ignores it reports standard errors that are too small and p-values that look stronger than the data support.

**Forms:** overdispersion, overdispersed, over-dispersion

**See also:** [`generalized-linear-model`](#term-generalized-linear-model), [`standard-error`](#term-standard-error), [`variance`](#term-variance)

<a id="term-overfitting"></a>

## overfitting

When a model learns the noise in its data instead of the real pattern.

**Why care:** An overfit model looks excellent on the data it was built from and disappoints on new data. Fewer predictors or more rows help.

**Forms:** overfitting, overfit, over-fitting

**See also:** [`r-squared`](#term-r-squared), [`sample-size`](#term-sample-size)

<a id="term-p-value"></a>

## p-value

The probability of a result at least this extreme if the test's null hypothesis (no difference, no link, a normal shape...) and its assumptions were all true.

**Why care:** Small values (below your alpha) mean the data would be surprising if there were no difference, which counts as evidence against 'no difference'. It is not the chance that the difference is real, and it says nothing about how BIG it is.

**Forms:** p-value, p-values, p value, p values

**See also:** [`effect-size`](#term-effect-size), [`alpha`](#term-alpha), [`multiple-comparisons`](#term-multiple-comparisons)

<a id="term-paired-data"></a>

## paired-data

Two measurements that belong together, such as the same customer before and after a change, held on one row.

**Why care:** Pairing removes the differences between subjects, so paired tests are more sensitive. Treating paired data as two separate groups wastes that and breaks the independence assumption.

**Forms:** paired data, paired samples, paired measurements

**See also:** [`independence`](#term-independence), [`repeated-measures`](#term-repeated-measures)

<a id="term-pairwise-deletion"></a>

## pairwise-deletion

Using every row that has both fields of a given pair, so each pair of fields may rest on a different set of rows.

**Why care:** It keeps more data than listwise deletion, but figures computed from different rows may not fit together consistently.

**Forms:** pairwise deletion

**See also:** [`listwise-deletion`](#term-listwise-deletion), [`missing-value`](#term-missing-value), [`correlation`](#term-correlation)

<a id="term-partial-correlation"></a>

## partial-correlation

The correlation between two measures after the straight-line part of chosen other measures has been taken out of both.

**Why care:** It separates a direct link from one that appears only because both measures follow a third; like any correlation it shows association, not cause.

**Forms:** partial correlation, partial correlations, partial r

**See also:** [`correlation`](#term-correlation), [`multicollinearity`](#term-multicollinearity)

<a id="term-partial-eta-squared"></a>

## partial-eta-squared

Eta-squared computed after setting aside variation explained by other factors, such as differences between the people measured repeatedly.

**Why care:** Used for repeated-measures designs. It is not comparable with plain eta-squared, so compare like with like.

**Forms:** partial eta squared, partial eta-squared, partial_eta_squared

**See also:** [`eta-squared`](#term-eta-squared), [`effect-size`](#term-effect-size)

<a id="term-percentage-point"></a>

## percentage-point

The plain difference between two percentages: 40% to 45% is a rise of 5 percentage points, which is a 12.5% relative rise.

**Why care:** Mixing up points and percent changes overstates or understates a change; say which one a figure is.

**Forms:** percentage point, percentage points

**See also:** [`index-value`](#term-index-value)

<a id="term-percentile"></a>

## percentile

The value at or below which about a given share of rows fall (about 90% sit at or below the 90th); a percentile rank is the reverse, a row's position as a percentage.

**Why care:** Percentiles describe a spread without assuming any shape, and are the honest way to report things like "most users wait less than X".

**See also:** [`median`](#term-median), [`outlier`](#term-outlier), [`rank`](#term-rank)

<a id="term-phi"></a>

## phi

The strength of the relationship in a two-by-two table, from 0 (unrelated) to 1 (perfectly related).

**Why care:** It is the yes/no-by-yes/no special case of Cramer's V, and reads like a correlation between two binary fields.

**Forms:** phi, phi coefficient

**See also:** [`cramers-v`](#term-cramers-v), [`chi-square`](#term-chi-square), [`correlation`](#term-correlation)

<a id="term-post-hoc-test"></a>

## post-hoc-test

A follow-up test run after an overall test is significant, to find which specific pairs of groups differ.

**Why care:** An overall test only says "some group differs". Post-hoc tests say which ones, and they correct for running many pairwise comparisons.

**Forms:** post-hoc, post hoc

**See also:** [`multiple-comparisons`](#term-multiple-comparisons), [`f-statistic`](#term-f-statistic)

<a id="term-posterior"></a>

## posterior

In a Bayesian analysis, the distribution of a parameter after combining the prior with the data, given the model.

**Why care:** Point estimates (such as the posterior mean) and credible intervals are summaries of it. It is only as trustworthy as the prior and the model that produced it.

**Forms:** posterior, posterior distribution, posterior mean, posterior means

**See also:** [`prior`](#term-prior), [`credible-interval`](#term-credible-interval)

<a id="term-principal-component"></a>

## principal-component

A new combined measure built from many correlated fields so that a few of them capture most of the variation.

**Why care:** It shrinks dozens of overlapping ratings into a handful of summary scores, making patterns easier to see and model.

**Forms:** principal component, principal components, pca

**See also:** [`eigenvalue`](#term-eigenvalue), [`loading`](#term-loading), [`factor`](#term-factor)

<a id="term-prior"></a>

## prior

In a Bayesian analysis, the distribution that states what values a parameter is believed to take before the data are seen.

**Why care:** The result blends the prior with the data. A weak prior lets the data dominate; a strong or badly chosen one pulls estimates toward it, so always report which prior was used.

**Forms:** prior distribution, prior distributions, prior belief, prior beliefs

**See also:** [`posterior`](#term-posterior), [`credible-interval`](#term-credible-interval)

<a id="term-probit"></a>

## probit

A transform that maps a proportion to the z-score at which the normal curve has that share below it: 0.5 maps to 0, 0.84 to about 1.

**Why care:** It stretches proportions near 0 and 1, where small changes in share matter more; tests built on it are a convention some survey tools follow.

**Forms:** probit

**See also:** [`z-score`](#term-z-score), [`normal-distribution`](#term-normal-distribution)

<a id="term-pseudo-r-squared"></a>

## pseudo-r-squared

A fit summary for models such as logistic regression where ordinary R-squared does not apply; Pulse's is one minus deviance over null deviance.

**Why care:** It is not a share of variance explained and runs much lower than R-squared for an equally useful model, so R-squared rules of thumb do not carry over. Compare it only across models of the same outcome.

**Forms:** pseudo-r-squared, pseudo r-squared, pseudo r squared, pseudo-r², pseudo_r2, mcfadden

**See also:** [`deviance`](#term-deviance), [`r-squared`](#term-r-squared), [`logistic-regression`](#term-logistic-regression)

<a id="term-r-squared"></a>

## r-squared

The share of the variation in an outcome that a model explains, from 0 (nothing) to 1 (everything).

**Why care:** Higher means the model tracks the outcome more closely, but it always rises as you add predictors, even useless ones; watch for overfitting.

**Forms:** r-squared, r squared, r², r2, coefficient of determination

**See also:** [`residual`](#term-residual), [`overfitting`](#term-overfitting), [`correlation`](#term-correlation)

<a id="term-raking"></a>

## raking

Adjusting weights step by step until the weighted sample matches known population totals on several characteristics at once.

**Why care:** It is the standard way to make a survey sample look like the population on age, region and the like, without needing every combination's total.

**Forms:** raking, rake, iterative proportional fitting

**See also:** [`weighting`](#term-weighting), [`effective-sample-size`](#term-effective-sample-size)

<a id="term-rank"></a>

## rank

A value's position once all values are sorted: 1 for the smallest, 2 for the next, and so on.

**Why care:** Working on ranks instead of raw values makes a method far less sensitive to extreme values and usable on ordinal ratings.

**See also:** [`non-parametric`](#term-non-parametric), [`percentile`](#term-percentile), [`rank-biserial`](#term-rank-biserial)

<a id="term-rank-biserial"></a>

## rank-biserial

An effect size for rank tests: how much more often a row from one group outranks a row from the other, from -1 to 1.

**Why care:** It gives Mann-Whitney and Wilcoxon results a size and a direction; zero means neither side tends to rank higher.

**Forms:** rank-biserial, rank biserial, rank_biserial

**See also:** [`rank`](#term-rank), [`non-parametric`](#term-non-parametric), [`effect-size`](#term-effect-size)

<a id="term-regression-coefficient"></a>

## regression-coefficient

How much the predicted outcome changes when one predictor goes up by one unit, holding the other predictors fixed.

**Why care:** It is the "how much" of a driver. Its size depends on the predictor's units, and with correlated predictors it can be unstable.

**Forms:** regression coefficient, regression coefficients

**See also:** [`standard-error`](#term-standard-error), [`multicollinearity`](#term-multicollinearity), [`r-squared`](#term-r-squared), [`odds-ratio`](#term-odds-ratio)

<a id="term-reliability"></a>

## reliability

How consistently a set of questions measures the same thing.

**Why care:** A scale built from unreliable questions mixes signal with noise, so averages and comparisons based on it are weaker.

**See also:** [`factor`](#term-factor), [`correlation`](#term-correlation)

<a id="term-repeated-measures"></a>

## repeated-measures

A design where the same subjects are measured several times, under different conditions or at different moments.

**Why care:** Measurements from one subject are related, so tests for independent groups misjudge the noise. Repeated-measures tests separate subject-to-subject differences from the effect of the condition.

**Forms:** repeated measures, repeated-measures, within-subject

**See also:** [`paired-data`](#term-paired-data), [`sphericity`](#term-sphericity), [`independence`](#term-independence)

<a id="term-residual"></a>

## residual

The gap between an actual value and what a model predicted for it.

**Why care:** Patterns in the residuals reveal what the model misses; large ones flag rows the model fits badly.

**Forms:** residual, residuals

**See also:** [`r-squared`](#term-r-squared), [`outlier`](#term-outlier)

<a id="term-rolling-mean"></a>

## rolling-mean

The average of the last few points in a series (a moving average), recomputed at every step.

**Why care:** It smooths out short-term swings, so a point far from its rolling mean stands out; a short window reacts fast but is jumpy.

**Forms:** rolling mean, rolling average, moving average

**See also:** [`mean`](#term-mean), [`baseline`](#term-baseline)

<a id="term-sample-size"></a>

## sample-size

How many rows (or respondents, or measurements) an estimate or test is based on.

**Why care:** Small samples give noisy estimates and weak tests; very large ones make tiny, unimportant differences significant.

**See also:** [`standard-error`](#term-standard-error), [`statistical-power`](#term-statistical-power), [`effective-sample-size`](#term-effective-sample-size)

<a id="term-similarity"></a>

## similarity

A number for how alike two rows or items are; the mirror image of distance.

**Why care:** It drives "customers like this one" style matching. Different measures weigh shared absences differently, so pick one that fits your data.

**See also:** [`distance`](#term-distance), [`correlation`](#term-correlation)

<a id="term-skew"></a>

## skew

How lopsided a distribution is: a long tail to the right (positive) or to the left (negative).

**Why care:** Skewed data pull the mean toward the tail, so the median often describes them better, and tests assuming a normal shape suffer.

**Forms:** skew, skewed, skewness

**See also:** [`normal-distribution`](#term-normal-distribution), [`median`](#term-median), [`kurtosis`](#term-kurtosis)

<a id="term-spearman-rho"></a>

## spearman-rho

A rank correlation from -1 to 1: Pearson's correlation computed on the ranks of the values instead of the values themselves.

**Why care:** It captures any relationship that keeps going one way, straight or curved, and resists outliers; it misses U-shaped links.

**Forms:** spearman's rho, spearman rho, spearman correlation

**See also:** [`correlation`](#term-correlation), [`kendall-tau`](#term-kendall-tau), [`rank`](#term-rank), [`monotonic-trend`](#term-monotonic-trend)

<a id="term-sphericity"></a>

## sphericity

In a repeated-measures design, the assumption that the differences between every pair of conditions are about equally variable.

**Why care:** When it fails, the repeated-measures F-test rejects too often. Pulse applies no correction for it, so the p-value can come out well below its true value; treat any result short of a very small p with caution.

**Forms:** sphericity

**See also:** [`repeated-measures`](#term-repeated-measures), [`f-statistic`](#term-f-statistic), [`homogeneity-of-variance`](#term-homogeneity-of-variance)

<a id="term-standard-deviation"></a>

## standard-deviation

The typical distance of a value from the mean, in the same units as the data.

**Why care:** It tells you how spread out values are. Two groups with the same mean can behave very differently if their standard deviations differ.

**Forms:** standard deviation, standard deviations

**See also:** [`variance`](#term-variance), [`z-score`](#term-z-score), [`standard-error`](#term-standard-error)

<a id="term-standard-error"></a>

## standard-error

How much an estimate such as a mean would wobble from sample to sample; it shrinks as you add rows.

**Why care:** It is the uncertainty of the estimate, not the spread of the data. Confidence intervals and most test statistics are built from it.

**Forms:** standard error, standard errors

**See also:** [`standard-deviation`](#term-standard-deviation), [`confidence-interval`](#term-confidence-interval), [`sample-size`](#term-sample-size)

<a id="term-statistical-power"></a>

## statistical-power

The chance a test detects a difference that really exists.

**Why care:** Small samples have low power, so a non-significant result from them is weak evidence of no difference: the test may simply have been unable to see it.

**Forms:** statistical power

**See also:** [`sample-size`](#term-sample-size), [`statistical-significance`](#term-statistical-significance), [`effect-size`](#term-effect-size)

<a id="term-statistical-significance"></a>

## statistical-significance

A result is statistically significant when its p-value falls below your chosen alpha.

**Why care:** Significant means data this extreme would be unusual if there were truly no effect; it is not the chance the effect is real, nor that it is important. With enough rows a tiny difference becomes significant; check the effect size (for a shape test, the statistic itself).

**Forms:** statistically significant, statistical significance

**See also:** [`p-value`](#term-p-value), [`alpha`](#term-alpha), [`effect-size`](#term-effect-size), [`statistical-power`](#term-statistical-power)

<a id="term-steady-state"></a>

## steady-state

The mix of states a flow settles into if the same switching probabilities keep applying period after period.

**Why care:** It shows where shares are heading in the long run, assuming nothing about the switching behaviour changes.

**Forms:** steady state, steady-state, stationary distribution

**See also:** [`stochastic-matrix`](#term-stochastic-matrix)

<a id="term-stochastic-matrix"></a>

## stochastic-matrix

A table of probabilities of moving from each state (row) to each state (column), where every row adds up to 1.

**Why care:** It describes flows such as customers switching brands or plans from one period to the next, and lets you project them forward.

**Forms:** stochastic matrix, transition matrix, transition matrices

**See also:** [`steady-state`](#term-steady-state)

<a id="term-studentized-range"></a>

## studentized-range

The distribution of the gap between the largest and smallest of several group means, measured in standard errors.

**Why care:** Tukey's test reads its pairwise p-values from it, which is how it accounts for comparing every pair at once rather than one pair alone.

**Forms:** studentized range, studentised range

**See also:** [`post-hoc-test`](#term-post-hoc-test), [`multiple-comparisons`](#term-multiple-comparisons), [`standard-error`](#term-standard-error)

<a id="term-t-statistic"></a>

## t-statistic

The test statistic of a t-test: a difference in means divided by its standard error.

**Why care:** Bigger in absolute size means the difference is large relative to its standard error, which shrinks as rows are added, so a big t can come from a tiny difference in a large sample. Its sign tells you which side is higher.

**Forms:** t-statistic, t statistic

**See also:** [`test-statistic`](#term-test-statistic), [`standard-error`](#term-standard-error), [`degrees-of-freedom`](#term-degrees-of-freedom)

<a id="term-test-statistic"></a>

## test-statistic

The single number a test computes from your data to measure how far it sits from the null hypothesis.

**Why care:** It is the raw ingredient of the p-value. Its scale differs from test to test, so compare effect sizes, not statistics or p-values, across tests; a p-value also depends on the sample size.

**Forms:** test statistic, test statistics

**See also:** [`p-value`](#term-p-value), [`t-statistic`](#term-t-statistic), [`f-statistic`](#term-f-statistic), [`chi-square`](#term-chi-square)

<a id="term-ties"></a>

## ties

Values that are exactly equal, so they share a rank.

**Why care:** Rank-based tests give tied values their average rank and correct for them, but many ties (as on short rating scales) weaken the test and make its large-sample p-value less accurate.

**Forms:** ties, tied values, tie correction

**See also:** [`rank`](#term-rank), [`non-parametric`](#term-non-parametric)

<a id="term-two-tailed"></a>

## two-tailed

A two-tailed (two-sided) test looks for a difference in either direction; a one-tailed test looks in only one direction chosen in advance.

**Why care:** Pulse's tests report two-sided p-values. Halving one to claim a one-sided result after seeing which way the data went inflates false alarms.

**Forms:** two-tailed, two-sided, one-tailed, one-sided

**See also:** [`p-value`](#term-p-value), [`alpha`](#term-alpha)

<a id="term-variance"></a>

## variance

Roughly the average squared distance of values from their mean: divided by n in the population form, or by n - 1 in the sample form most tools print.

**Why care:** A measure of spread in squared units. Most tests are built on the sample form, but its squared units are hard to read; the standard deviation is the same idea in the original units.

**Forms:** variance, variances

**See also:** [`standard-deviation`](#term-standard-deviation), [`covariance`](#term-covariance)

<a id="term-weighting"></a>

## weighting

Giving some rows more say than others in a figure, usually so a sample better matches the population it stands for.

**Why care:** Weights fix known imbalances, but heavy weights make results noisier; check the effective sample size.

**See also:** [`raking`](#term-raking), [`effective-sample-size`](#term-effective-sample-size)

<a id="term-z-score"></a>

## z-score

How many standard deviations a value sits above or below the mean. A z test statistic is the same idea for an estimate: how many standard errors it sits from the null's value.

**Why care:** It puts values from different scales on one footing and makes unusual values easy to spot.

**Forms:** z-score, z-scores, z score, z scores

**See also:** [`standard-deviation`](#term-standard-deviation), [`outlier`](#term-outlier), [`normal-distribution`](#term-normal-distribution)
