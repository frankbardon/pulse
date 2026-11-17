package regordinal

// export_test.go exposes the golden loader to the package's external
// tests (ordinal_e2e_test.go), which import the root package and so
// cannot live in this package.

type OracleCase = oracleCase

var LoadOracle = loadOracle

type OracleModifiers = oracleModifiers

var LoadModifiers = loadModifiers

type MarginsOracleCase = marginsOracleCase

var LoadMarginsOracle = loadMarginsOracle

type OracleNominal = oracleNominal

var NominalErr = nominalErr

var NominalReasons = nominalReasons

const NominalRel = nominalRel

// AuxTestFits is how many fits so far ran the Brant or the nominal test.
func AuxTestFits() int64 { return auxTestFits.Load() }
