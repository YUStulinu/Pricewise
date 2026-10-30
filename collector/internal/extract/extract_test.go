package extract

import (
	"strings"
	"testing"
)

func page(ld string) string {
	return `<html><head><title>x</title><script type="application/ld+json">` + ld +
		`</script></head><body><p>9.99</p></body></html>`
}

func TestShapesOfMarkup(t *testing.T) {
	cases := []struct {
		name, ld string
		want     Product
	}{
		{"offer with strikethrough",
			`{"@context":"https://schema.org","@type":"Product","name":"WD Blue 1TB","brand":{"@type":"Brand","name":"WD"},"sku":"A1",
			 "offers":{"@type":"Offer","price":"79.99","priceCurrency":"EUR","availability":"https://schema.org/InStock",
			 "priceSpecification":[{"@type":"UnitPriceSpecification","priceType":"https://schema.org/StrikethroughPrice","price":"99.99","priceCurrency":"EUR"}]}}`,
			Product{Name: "WD Blue 1TB", Brand: "WD", SKU: "A1", Price: 79.99, Currency: "EUR", ReferencePrice: 99.99, Available: true}},
		{"graph, offers array, numeric price, list price",
			`{"@context":"https://schema.org","@graph":[{"@type":"WebPage"},{"@type":"Product","name":"Cam","gtin13":"4006381333931",
			 "offers":[{"@type":"Offer","price":45,"priceCurrency":"EUR","availability":"https://schema.org/OutOfStock",
			 "priceSpecification":{"@type":"UnitPriceSpecification","priceType":"https://schema.org/ListPrice","price":60}}]}]}`,
			Product{Name: "Cam", GTIN: "4006381333931", Price: 45, Currency: "EUR", ReferencePrice: 60, Available: false}},
		{"price only in a specification, comma decimals",
			`[{"@type":"Organization"},{"@type":["Product"],"name":"Pen",
			 "offers":{"@type":"Offer","priceSpecification":[{"@type":"UnitPriceSpecification","price":"1.299,50","priceCurrency":"EUR"}]}}]`,
			Product{Name: "Pen", Price: 1299.5, Currency: "EUR", Available: true}},
		{"a was-price that is not higher is ignored",
			`{"@type":"Product","name":"Mug","offers":{"@type":"Offer","price":"5","priceCurrency":"EUR",
			 "priceSpecification":{"@type":"UnitPriceSpecification","priceType":"https://schema.org/StrikethroughPrice","price":"5"}}}`,
			Product{Name: "Mug", Price: 5, Currency: "EUR", Available: true}},
	}
	for _, c := range cases {
		got, err := FromHTML(strings.NewReader(page(c.ld)))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

func TestNoProduct(t *testing.T) {
	for _, doc := range []string{
		page(`{"@type":"Organization","name":"Shop"}`),
		page(`{not json`),
		page(`{"@type":"Product","name":"No price","offers":{"@type":"Offer"}}`),
		`<html><body>no markup</body></html>`,
	} {
		if _, err := FromHTML(strings.NewReader(doc)); err != ErrNoProduct {
			t.Errorf("want ErrNoProduct, got %v for %s", err, doc)
		}
	}
}

func TestSecondBlockIsRead(t *testing.T) {
	doc := `<script type="application/ld+json">{"@type":"BreadcrumbList"}</script>` +
		page(`{"@type":"Product","name":"B","offers":{"price":"2","priceCurrency":"EUR"}}`)
	got, err := FromHTML(strings.NewReader(doc))
	if err != nil || got.Name != "B" || got.Price != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}
