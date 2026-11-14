// mmm_bayes_geo.stan — REG_MMM_BAYES's geo-hierarchical model
// (params.geo; internal/regmmm/bayes_geo_model.go) written independently
// in Stan, for scripts/reference/gen_mmm_bayes_geo_stan.R. Geometric
// adstock and the Hill curve, ROI mode, default priors.
//
// Every figure is on Pulse's internally scaled data, the whole panel's
// scaling (one figure means the same in every geo), which the R script
// computes exactly as Pulse does and passes in:
//
//   ys_gt     = y_gt / max|y|                      (over the panel)
//   a_gct     = sum_{l=0..min(t, L)} decay_gc^l * spend_gc,t-l
//   h_gct     = 1 / (1 + (k_gc / (a_gct / max spend_c))^s_gc), 0 at a = 0
//   zs_gjt    = (z_gjt - mean z_j) / max|z_j - mean z_j|   (panel mean, max)
//   ys_gt     ~ Normal(alpha_g + sum_j gamma_j zs_gjt + sum_c beta_gc h_gct, sigma)
//   beta_gc   = roi_gc * share_gc / sum_t h_gct    (share_gc = sum_t spend_gct / max|y|)
//
// with an intercept per geo, shared controls and one sigma. Each pooled
// class — the channel's ROI and its decay, and with per_geo_curves its
// Hill k and s — is non-centred on its prior's unconstrained scale:
//
//   roi_gc   = exp(log(mu_roi_c) + tau_roi_c * eta_roi_cg)
//   decay_gc = inv_logit(logit(mu_decay_c) + tau_decay_c * eta_decay_cg)
//   k_gc     = 0.02 + 9.98 * inv_logit(logit((mu_k_c - 0.02) / 9.98) + tau_k_c * eta_k_cg)
//   s_gc     = exp(log(mu_s_c) + tau_s_c * eta_s_cg)
//   eta ~ Normal(0, 1), tau ~ HalfNormal(0.5)
//
// mu carries the one-market prior (decay Beta(1, 1), k TruncNormal(1, 1,
// 0.02, 10), s LogNormal(0.3, 0.5), ROI LogNormal(0.2, 0.9)); alpha
// Normal(0, 2), gamma Normal(0, 1), sigma HalfNormal(1). Without
// per_geo_curves (data 0) every geo reads mu_k and mu_s. Stan's bound
// transforms add their own Jacobians, so the posterior is Pulse's
// (normalising constants of fixed truncations aside). The curve values h
// are model-block locals (recomputed in generated quantities), so the
// draws file carries only parameters and reported figures.
functions {
  // curve is one geo-channel's h_t: geometric adstock of the raw spend x
  // at decay over lags 0..L, scaled by xscale, through Hill(k, s).
  vector curve(vector x, real decay, real k, real s, real xscale, int L) {
    int N = num_elements(x);
    vector[N] h;
    for (t in 1:N) {
      real a = 0;
      for (l in 0:min(t - 1, L)) {
        a += decay ^ l * x[t - l];
      }
      a /= xscale;
      h[t] = a <= 0 ? 0 : 1 / (1 + (k / a) ^ s);
    }
    return h;
  }
}
data {
  int<lower=1> G;
  int<lower=1> N; // weeks per geo
  int<lower=1> C;
  int<lower=0> J;
  int<lower=1> L;
  int<lower=0, upper=1> per_geo_curves;
  array[G] vector[N] ys;
  array[G, C] vector[N] spend;
  vector[C] xscale;
  array[G] vector[C] share;
  array[G] vector[C] spend_total;
  array[G, J] vector[N] zs;
  real yscale;
  vector[J] zmean;
  vector[J] zscale;
}
transformed data {
  int Cq = per_geo_curves ? C : 0;
  vector[C] spend_all = rep_vector(0, C);
  for (g in 1:G) spend_all += spend_total[g];
}
parameters {
  vector[G] alpha;
  vector[J] gamma;
  vector<lower=0, upper=1>[C] mu_decay;
  vector<lower=0.02, upper=10>[C] mu_k;
  vector<lower=0>[C] mu_s;
  vector<lower=0>[C] mu_roi;
  real<lower=0> sigma;
  vector<lower=0>[C] tau_roi;
  vector<lower=0>[C] tau_decay;
  vector<lower=0>[Cq] tau_k;
  vector<lower=0>[Cq] tau_s;
  array[C] vector[G] eta_roi;
  array[C] vector[G] eta_decay;
  array[Cq] vector[G] eta_k;
  array[Cq] vector[G] eta_s;
}
transformed parameters {
  array[C] vector[G] roi;
  array[C] vector[G] decay;
  array[C] vector[G] k;
  array[C] vector[G] s;
  for (c in 1:C) {
    roi[c] = exp(log(mu_roi[c]) + tau_roi[c] * eta_roi[c]);
    decay[c] = inv_logit(logit(mu_decay[c]) + tau_decay[c] * eta_decay[c]);
    if (per_geo_curves) {
      k[c] = 0.02 + 9.98 * inv_logit(logit((mu_k[c] - 0.02) / 9.98) + tau_k[c] * eta_k[c]);
      s[c] = exp(log(mu_s[c]) + tau_s[c] * eta_s[c]);
    } else {
      k[c] = rep_vector(mu_k[c], G);
      s[c] = rep_vector(mu_s[c], G);
    }
  }
}
model {
  alpha ~ normal(0, 2);
  gamma ~ normal(0, 1);
  mu_decay ~ beta(1, 1);
  mu_k ~ normal(1, 1);
  mu_s ~ lognormal(0.3, 0.5);
  mu_roi ~ lognormal(0.2, 0.9);
  sigma ~ normal(0, 1);
  tau_roi ~ normal(0, 0.5);
  tau_decay ~ normal(0, 0.5);
  tau_k ~ normal(0, 0.5);
  tau_s ~ normal(0, 0.5);
  for (c in 1:C) {
    eta_roi[c] ~ std_normal();
    eta_decay[c] ~ std_normal();
  }
  for (c in 1:Cq) {
    eta_k[c] ~ std_normal();
    eta_s[c] ~ std_normal();
  }
  for (g in 1:G) {
    vector[N] mu = rep_vector(alpha[g], N);
    for (j in 1:J) mu += gamma[j] * zs[g, j];
    for (c in 1:C) {
      vector[N] h = curve(spend[g, c], decay[c][g], k[c][g], s[c][g], xscale[c], L);
      mu += roi[c][g] * share[g][c] / sum(h) * h;
    }
    ys[g] ~ normal(mu, sigma);
  }
}
generated quantities {
  // Pulse's geo coefficient table in original units
  // (bayesGeoModel.draw): each geo's intercept on the raw outcome; per
  // channel the population beta — mu_roi's ROI at the population decay
  // and curve over the whole panel — and each geo's beta; the controls;
  // decay (population, per geo); k in raw adstock units and s (shared,
  // or population and per geo); sigma on the raw outcome; and the
  // all-geo ROI (summed contribution over summed spend).
  vector[G] intercept_raw;
  vector[C] beta_pop_raw;
  array[G] vector[C] beta_raw;
  vector[J] gamma_raw;
  vector[C] mu_k_raw = mu_k .* xscale;
  array[C] vector[G] k_raw;
  real sigma_raw = yscale * sigma;
  vector[C] roi_all;
  for (g in 1:G) {
    intercept_raw[g] = alpha[g];
    for (j in 1:J) intercept_raw[g] -= gamma[j] * zmean[j] / zscale[j];
    intercept_raw[g] *= yscale;
    for (c in 1:C) {
      beta_raw[g][c] = yscale * roi[c][g] * share[g][c] / sum(curve(spend[g, c], decay[c][g], k[c][g], s[c][g], xscale[c], L));
    }
  }
  for (j in 1:J) gamma_raw[j] = yscale * gamma[j] / zscale[j];
  for (c in 1:C) {
    real htot = 0;
    real contrib = 0;
    for (g in 1:G) {
      htot += sum(curve(spend[g, c], mu_decay[c], mu_k[c], mu_s[c], xscale[c], L));
      contrib += roi[c][g] * spend_total[g][c];
    }
    beta_pop_raw[c] = yscale * mu_roi[c] * (spend_all[c] / yscale) / htot;
    k_raw[c] = k[c] * xscale[c];
    roi_all[c] = contrib / spend_all[c];
  }
}
