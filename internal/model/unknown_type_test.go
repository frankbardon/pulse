package model

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func TestUnknownRegressionType_Shape(t *testing.T) {
	cerr := UnknownRegressionType(2, "REG_NOPE")
	if cerr.Code != errors.PULSE_REGRESSION_INVALID ||
		cerr.Message != "regression[2]: unknown regression type REG_NOPE" ||
		cerr.Details["regression_index"] != 2 || cerr.Details["type"] != "REG_NOPE" {
		t.Fatalf("refusal = %#v", cerr)
	}
}
