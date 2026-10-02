package arrow

import (
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// TestFormatValue_Date32 pins the epoch-day → ISO rendering of a Date32
// cell, including the pre-epoch side of the sign boundary.
func TestFormatValue_Date32(t *testing.T) {
	b := array.NewDate32Builder(memory.NewGoAllocator())
	defer b.Release()
	days := []int32{0, 1, -1, -25567, 19787}
	want := []string{"1970-01-01", "1970-01-02", "1969-12-31", "1900-01-01", "2024-03-05"}
	for _, d := range days {
		b.Append(arrow.Date32(d))
	}
	arr := b.NewArray()
	defer arr.Release()
	for i := range days {
		if got := FormatValue(arr, i); got != want[i] {
			t.Errorf("FormatValue(Date32 %d) = %q, want %q", days[i], got, want[i])
		}
	}
}
