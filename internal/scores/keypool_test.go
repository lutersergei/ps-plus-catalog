package scores

import "testing"

func TestKeyPoolRotation(t *testing.T) {
	p := NewKeyPool([]string{"a", "", "b", "a"}) // пустые строки отбрасываются, дубли — нет (3 ключа: a, b, a)
	if p.Count() != 3 {
		t.Fatalf("ожидали 3 ключа (a,b,a), получили %d", p.Count())
	}
	k, ok := p.current()
	if !ok || k != "a" {
		t.Fatalf("первый ключ a, получили %q ok=%v", k, ok)
	}
	p.markExhausted() // a -> b
	if k, ok := p.current(); !ok || k != "b" {
		t.Fatalf("после ротации ждали b, получили %q ok=%v", k, ok)
	}
	p.markExhausted() // b -> a(3-й)
	if k, ok := p.current(); !ok || k != "a" {
		t.Fatalf("ждали третий ключ a, получили %q ok=%v", k, ok)
	}
	p.markExhausted() // все исчерпаны
	if _, ok := p.current(); ok {
		t.Fatal("ожидали, что ключей не осталось")
	}
	if NewKeyPool(nil).Empty() != true {
		t.Fatal("пустой пул должен быть Empty")
	}
}
