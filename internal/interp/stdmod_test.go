package interp

import "testing"

func TestHexVal(t *testing.T) {
	for _, c := range []byte{'0', '9', 'A', 'E', 'F', 'a', 'f'} {
		v, ok := hexVal(c)
		if !ok {
			t.Fatalf("hexVal(%q) should succeed", c)
		}
		t.Logf("hexVal(%q) = %d", c, v)
	}
	if _, ok := hexVal('G'); ok {
		t.Fatal("hexVal('G') should fail")
	}
}

func TestPercentDecode(t *testing.T) {
	m := modEncoding()
	v, ok := m.Get("s:网址解码")
	if !ok {
		t.Fatal("网址解码 not registered")
	}
	b := v.(*Builtin)
	val, errE := b.Call(nil, []Value{"%E5%8D%85"}, nil, 1)
	if errE != nil {
		t.Fatalf("decode failed: %v", errE)
	}
	if val != "卅" {
		t.Fatalf("got %q", val)
	}
}
