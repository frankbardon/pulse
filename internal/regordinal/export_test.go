package regordinal

// export_test.go exposes the golden loader to the package's external
// tests (ordinal_e2e_test.go), which import the root package and so
// cannot live in this package.

type OracleCase = oracleCase

var LoadOracle = loadOracle
