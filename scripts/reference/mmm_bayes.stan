// mmm_bayes.stan — the REG_MMM_BAYES model (internal/regmmm/bayes_model.go)
// written independently in Stan, for scripts/reference/gen_mmm_bayes_stan.R.
//
// Every figure is on Pulse's internally scaled data, which the R script
// computes exactly as Pulse does and passes in:
//
//   ys_t      = y_t / max|y|
//   a_ct      = sum_{l=0..min(t, L)} w_l(phi_c) * spend_c,t-l   (raw spend)
//   h_ct      = curve(a_ct / max(spend_c), q_c)
//   zs_jt     = (z_jt - mean z_j) / max|z_j - mean z_j|
//   ys_t      ~ Normal(alpha + sum_j gamma_j zs_jt + sum_c beta_c h_ct, sigma)
//   beta_c    = roi_c * share_c / sum_t h_ct     (ROI mode, every channel)
//
// The adstock kernel (data `kernel`): 0 geometric decay^l, 1 delayed
// decay^((l - peak)^2), 2 weibull_cdf exp(-(l/scale)^shape), 3 weibull_pdf
// the Weibull density at l + 1. One kernel for every channel; a kernel's
// unused parameter vectors have size 0. The curve (data `curve`, one for
// every channel, on the scaled adstock a): 0 Hill 1 / (1 + (k/a)^s), 0 at
// a = 0; 1 logistic (1 - exp(-lam a)) / (1 + exp(-lam a)); 2
// Michaelis-Menten a / (k + a). A curve's unused parameter vectors have
// size 0.
//
// Priors are REG_MMM_BAYES's defaults: decay Beta(1, 1), peak
// TruncNormal(1, 2, 0, L), shape LogNormal(0, 0.5), scale LogNormal(1,
// 0.75), k TruncNormal(1, 1, 0.02, 10) (Hill and Michaelis-Menten), s
// LogNormal(0.3, 0.5), lam LogNormal(0, 1), ROI
// LogNormal(0.2, 0.9), alpha Normal(0, 2), gamma Normal(0, 1), sigma
// HalfNormal(1). Stan's bound transforms add their own Jacobians, so the
// posterior is Pulse's (normalising constants of fixed truncations
// aside, which no posterior mean sees).
data {
  int<lower=1> N;
  int<lower=1> C;
  int<lower=0> J;
  int<lower=1> L;
  int<lower=0, upper=3> kernel;
  int<lower=0, upper=2> curve;
  vector[N] ys;
  array[C] vector[N] spend;
  vector[C] xscale;
  vector[C] share;
  array[J] vector[N] zs;
  real yscale;
  vector[J] zmean;
  vector[J] zscale;
}
transformed data {
  int Cd = (kernel <= 1) ? C : 0;
  int Cp = (kernel == 1) ? C : 0;
  int Cw = (kernel >= 2) ? C : 0;
  int Ck = (curve != 1) ? C : 0;
  int Cs = (curve == 0) ? C : 0;
  int Cl = (curve == 1) ? C : 0;
}
parameters {
  real alpha;
  vector[J] gamma;
  vector<lower=0, upper=1>[Cd] decay;
  vector<lower=0, upper=L>[Cp] peak;
  vector<lower=0>[Cw] wshape;
  vector<lower=0>[Cw] wscale;
  vector<lower=0.02, upper=10>[Ck] k;
  vector<lower=0>[Cs] s;
  vector<lower=0>[Cl] lam;
  vector<lower=0>[C] roi;
  real<lower=0> sigma;
}
transformed parameters {
  array[C] vector[N] h;
  vector[C] beta;
  for (c in 1:C) {
    vector[L + 1] w;
    for (l in 0:L) {
      if (kernel == 0) {
        w[l + 1] = decay[c] ^ l;
      } else if (kernel == 1) {
        w[l + 1] = decay[c] ^ square(l - peak[c]);
      } else if (kernel == 2) {
        w[l + 1] = exp(-((l / wscale[c]) ^ wshape[c]));
      } else {
        real zz = (l + 1) / wscale[c];
        w[l + 1] = wshape[c] / wscale[c] * zz ^ (wshape[c] - 1) * exp(-(zz ^ wshape[c]));
      }
    }
    for (t in 1:N) {
      real a = 0;
      for (l in 0:min(t - 1, L)) {
        a += w[l + 1] * spend[c][t - l];
      }
      a /= xscale[c];
      if (a <= 0) {
        h[c][t] = 0;
      } else if (curve == 0) {
        h[c][t] = 1 / (1 + (k[c] / a) ^ s[c]);
      } else if (curve == 1) {
        h[c][t] = (1 - exp(-lam[c] * a)) / (1 + exp(-lam[c] * a));
      } else {
        h[c][t] = a / (k[c] + a);
      }
    }
    beta[c] = roi[c] * share[c] / sum(h[c]);
  }
}
model {
  vector[N] mu = rep_vector(alpha, N);
  for (j in 1:J) mu += gamma[j] * zs[j];
  for (c in 1:C) mu += beta[c] * h[c];
  alpha ~ normal(0, 2);
  gamma ~ normal(0, 1);
  decay ~ beta(1, 1);
  peak ~ normal(1, 2);
  wshape ~ lognormal(0, 0.5);
  wscale ~ lognormal(1, 0.75);
  k ~ normal(1, 1);
  s ~ lognormal(0.3, 0.5);
  lam ~ lognormal(0, 1);
  roi ~ lognormal(0.2, 0.9);
  sigma ~ normal(0, 1);
  ys ~ normal(mu, sigma);
}
generated quantities {
  // Pulse's coefficient table in original units (bayesModel.constrain,
  // bayesModel.effects): the intercept and controls on the raw outcome,
  // each channel's beta on the raw outcome, k in raw adstock units, lam
  // per raw adstock unit, sigma on the raw outcome.
  real intercept_raw = alpha;
  vector[J] gamma_raw;
  vector[C] beta_raw = yscale * beta;
  vector[Ck] k_raw;
  vector[Cl] lam_raw;
  real sigma_raw = yscale * sigma;
  for (j in 1:J) {
    intercept_raw -= gamma[j] * zmean[j] / zscale[j];
    gamma_raw[j] = yscale * gamma[j] / zscale[j];
  }
  intercept_raw *= yscale;
  for (c in 1:Ck) k_raw[c] = k[c] * xscale[c];
  for (c in 1:Cl) lam_raw[c] = lam[c] / xscale[c];
}
