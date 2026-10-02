package cli

import (
	"bytes"
	"context"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// TestSchemaCommand_ServesDefaultInstance: `pulse schema` prints the
// default instance's payload schema — byte-identical to the
// full-registry BuildPayloadSchema (the published golden), digest
// $comment included — followed by a newline.
func TestSchemaCommand_ServesDefaultInstance(t *testing.T) {
	cmd := SchemaCommand()
	var buf bytes.Buffer
	cmd.Writer = &buf
	if err := cmd.Run(context.Background(), []string{"schema"}); err != nil {
		t.Fatalf("schema: %v", err)
	}
	want := append(append([]byte{}, descx.BuildPayloadSchema()...), '\n')
	if !bytes.Equal(buf.Bytes(), want) {
		t.Error("pulse schema output differs from BuildPayloadSchema")
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"$comment": "feature_set_digest: fs1:`)) {
		t.Error("pulse schema output carries no feature_set_digest $comment")
	}
}
