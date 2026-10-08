package docgen

import "testing"

func TestEscapePlaceholders(t *testing.T) {
	cases := []struct{ in, want string }{
		{"named <prefix>_<k> by default", "named &lt;prefix&gt;_&lt;k&gt; by default"},
		{"code `<field>_poly` stays", "code `<field>_poly` stays"},
		{"double ``a <k> ` b`` and <k>", "double ``a <k> ` b`` and &lt;k&gt;"},
		{"unclosed ` then <k>", "unclosed ` then &lt;k&gt;"},
		{"| a<br>b | <a id=\"x\"></a> <!-- docgen:x -->", "| a<br>b | <a id=\"x\"></a> <!-- docgen:x -->"},
		{"```text\n<field>\n```\n<field>", "```text\n<field>\n```\n&lt;field&gt;"},
		{"~~~~\n<k>\n~~~~", "~~~~\n<k>\n~~~~"},
		{"a <= b and x<y> <https://x.y>", "a <= b and x&lt;y&gt; <https://x.y>"},
	}
	for _, c := range cases {
		got := escapePlaceholders(c.in)
		if got != c.want {
			t.Errorf("escapePlaceholders(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
		if again := escapePlaceholders(got); again != got {
			t.Errorf("not idempotent on %q: %q", got, again)
		}
	}
}
