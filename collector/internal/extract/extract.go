// Package extract reads a product's price from the schema.org JSON-LD that shops put in their
// pages for search engines. That markup is structured and meant to be read by machines, so
// Pricewise never has to guess prices out of the visible HTML.
package extract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Product is what one product page says.
type Product struct {
	Name           string
	Brand          string
	SKU            string
	GTIN           string
	Price          float64
	Currency       string
	ReferencePrice float64 // the crossed-out "was" price; 0 if none
	Available      bool
}

var ErrNoProduct = errors.New("no schema.org Product with an Offer price in the page")

// FromHTML finds the first Product with a priced Offer in the page's JSON-LD blocks.
func FromHTML(r io.Reader) (Product, error) {
	z := html.NewTokenizer(r)
	inLD := false
	var buf bytes.Buffer
	for {
		switch z.Next() {
		case html.ErrorToken:
			if errors.Is(z.Err(), io.EOF) {
				return Product{}, ErrNoProduct
			}
			return Product{}, z.Err()
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "script" || !hasAttr {
				continue
			}
			for {
				k, v, more := z.TagAttr()
				if string(k) == "type" && strings.EqualFold(strings.TrimSpace(string(v)), "application/ld+json") {
					inLD = true
					buf.Reset()
				}
				if !more {
					break
				}
			}
		case html.TextToken:
			if inLD {
				buf.Write(z.Text())
			}
		case html.EndTagToken:
			if inLD {
				inLD = false
				if p, ok := fromJSON(buf.Bytes()); ok {
					return p, nil
				}
			}
		}
	}
}

func fromJSON(data []byte) (Product, bool) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return Product{}, false
	}
	for _, node := range nodes(v) {
		if !isType(node, "Product") {
			continue
		}
		if p, ok := product(node); ok {
			return p, true
		}
	}
	return Product{}, false
}

// nodes flattens a JSON-LD document: a single object, an array, or an "@graph".
func nodes(v any) []map[string]any {
	var out []map[string]any
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			out = append(out, nodes(x)...)
		}
	case map[string]any:
		out = append(out, t)
		if g, ok := t["@graph"]; ok {
			out = append(out, nodes(g)...)
		}
	}
	return out
}

func isType(node map[string]any, want string) bool {
	short := func(s string) string {
		return strings.TrimPrefix(strings.TrimPrefix(s, "https://schema.org/"), "http://schema.org/")
	}
	switch t := node["@type"].(type) {
	case string:
		return short(t) == want
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok && short(s) == want {
				return true
			}
		}
	}
	return false
}

func product(node map[string]any) (Product, bool) {
	p := Product{Name: str(node["name"]), SKU: str(node["sku"])}
	for _, k := range []string{"gtin13", "gtin", "gtin12", "gtin14", "gtin8"} {
		if s := str(node[k]); s != "" {
			p.GTIN = s
			break
		}
	}
	switch b := node["brand"].(type) {
	case string:
		p.Brand = strings.TrimSpace(b)
	case map[string]any:
		p.Brand = str(b["name"])
	}
	var offers []map[string]any
	switch o := node["offers"].(type) {
	case map[string]any:
		offers = append(offers, o)
		if isType(o, "AggregateOffer") {
			offers = append(offers, nodes(o["offers"])...)
		}
	case []any:
		offers = nodes(o)
	}
	for _, o := range offers {
		price, ok := number(o["price"])
		if !ok {
			price, ok = number(o["lowPrice"])
		}
		cur := str(o["priceCurrency"])
		var reference float64
		// The price may also live only in a priceSpecification.
		for _, spec := range nodes(o["priceSpecification"]) {
			v, has := number(spec["price"])
			if !has {
				continue
			}
			kind := str(spec["priceType"])
			if strings.HasSuffix(kind, "StrikethroughPrice") || strings.HasSuffix(kind, "ListPrice") {
				reference = v
			} else if !ok {
				price, ok = v, true
				if cur == "" {
					cur = str(spec["priceCurrency"])
				}
			}
		}
		if !ok {
			continue
		}
		p.Price, p.Currency = price, cur
		if reference > price { // a "was" price that is not higher is no discount claim
			p.ReferencePrice = reference
		}
		avail := str(o["availability"])
		p.Available = avail == "" || strings.HasSuffix(avail, "InStock") || strings.HasSuffix(avail, "LimitedAvailability") ||
			strings.HasSuffix(avail, "OnlineOnly")
		return p, p.Name != ""
	}
	return Product{}, false
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

// number reads a price given as a JSON number or a string ("1299.00", "1.299,00", "1,299.00").
func number(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		dot, comma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
		if comma > dot { // "1.299,00": the comma is the decimal separator
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else { // "1,299.00"
			s = strings.ReplaceAll(s, ",", "")
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	return 0, false
}
