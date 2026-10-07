package mcpserve_test

import (
	"context"
	stderrors "errors"
	"io"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/mcpserve"
	"github.com/spf13/afero"
)

// TestServe_DefaultReturnReachesRegister: Options.DefaultReturn is
// threaded into gosdk.Config, so an unknown preset fails Serve before
// any transport is read (PULSE_RETURN_INVALID).
func TestServe_DefaultReturnReachesRegister(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	err = mcpserve.Serve(context.Background(), p, mcpserve.Options{DefaultReturn: "lean"}, strings.NewReader(""), io.Discard)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_RETURN_INVALID {
		t.Errorf("Serve(lean) = %v, want PULSE_RETURN_INVALID", err)
	}
}
