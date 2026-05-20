package detect

import "testing"

func TestClassify_PositiveEnglish(t *testing.T) {
	got := Classify("Northwind is the best CRM for small teams; I would pick it every time.", "Northwind")
	if got != SentimentPositive {
		t.Fatalf("expected positive, got %s", got)
	}
}

func TestClassify_NegativeEnglish(t *testing.T) {
	got := Classify("I would avoid Northwind — it's the worst option here.", "Northwind")
	if got != SentimentNegative {
		t.Fatalf("expected negative, got %s", got)
	}
}

func TestClassify_NeutralEnglish(t *testing.T) {
	got := Classify("Northwind is a CRM tool released in 2024.", "Northwind")
	if got != SentimentNeutral {
		t.Fatalf("expected neutral, got %s", got)
	}
}

func TestClassify_PositiveJapanese(t *testing.T) {
	got := Classify("トヨタは最高でおすすめです。", "Toyota")
	if got != SentimentPositive {
		t.Fatalf("expected positive (最高+おすすめ), got %s", got)
	}
}

func TestClassify_NegativeJapanese(t *testing.T) {
	got := Classify("そのサービスは最悪で、避けたほうが良いです。", "Brand")
	if got != SentimentNegative {
		t.Fatalf("expected negative (最悪+避け), got %s", got)
	}
}

func TestClassify_DoesNotMatchSubwords(t *testing.T) {
	// "bestseller" contains "best" but shouldn't trigger positive on its own.
	// (We have one positive marker, total weight 3, but it's inside a longer
	// word — whole-word matching should reject it.)
	got := Classify("Northwind has a bestseller program.", "Northwind")
	if got == SentimentPositive {
		t.Fatalf("'bestseller' should not trigger positive on its own; got %s", got)
	}
}

func TestClassify_EmptyContext(t *testing.T) {
	if got := Classify("", "X"); got != SentimentNeutral {
		t.Fatalf("empty context should be neutral, got %s", got)
	}
}
