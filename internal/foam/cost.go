package foam

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

// PriceRow prices nodes by label. A blank field matches any value.
type PriceRow struct {
	InstanceType, Region, CapacityType string
	Hourly                             float64
}

// Prices is a --prices table in file order.
type Prices []PriceRow

var priceHeader = []string{"instance_type", "region", "capacity_type", "hourly_usd"}

// ParsePrices reads a CSV with priceHeader as its first row. Lines starting
// with # are comments.
func ParsePrices(r io.Reader) (Prices, error) {
	cr := csv.NewReader(r)
	cr.Comment = '#'
	cr.FieldsPerRecord = len(priceHeader)
	cr.TrimLeadingSpace = true
	records, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 || !slices.Equal(records[0], priceHeader) {
		return nil, fmt.Errorf("first row must be %s", strings.Join(priceHeader, ","))
	}
	if len(records) == 1 {
		return nil, errors.New("no prices")
	}
	var p Prices
	for i, rec := range records[1:] {
		h, err := strconv.ParseFloat(rec[3], 64)
		if err != nil || !(h >= 0) || math.IsInf(h, 1) {
			return nil, fmt.Errorf("row %d: hourly_usd must be a number, 0 or more, got %q", i+2, rec[3])
		}
		if c := rec[2]; c != "" && c != "spot" && c != "on-demand" {
			return nil, fmt.Errorf("row %d: capacity_type must be spot, on-demand or blank, got %q", i+2, c)
		}
		p = append(p, PriceRow{InstanceType: rec[0], Region: rec[1], CapacityType: rec[2], Hourly: h})
	}
	// Most specific first, file order on a tie, so Price takes the first match.
	slices.SortStableFunc(p, func(a, b PriceRow) int { return b.specificity() - a.specificity() })
	return p, nil
}

func (r PriceRow) specificity() int {
	n := 0
	for _, f := range []string{r.InstanceType, r.Region, r.CapacityType} {
		if f != "" {
			n++
		}
	}
	return n
}

// Price is the hourly price of the most specific row matching n, the first
// such row on a tie.
func (p Prices) Price(n Node) (float64, bool) {
	m := func(want, got string) bool { return want == "" || want == got }
	for _, r := range p {
		if m(r.InstanceType, n.InstanceType) && m(r.Region, n.Region) && m(r.CapacityType, n.CapacityType) {
			return r.Hourly, true
		}
	}
	return 0, false
}

// PodCost charges a pod its node's price times its larger share of the node,
// so a pod that fills the memory pays for the CPU nobody else can use. nil
// when the node has no price.
func PodCost(p Pod, n Node) *float64 {
	if n.HourlyPrice == nil {
		return nil
	}
	c := *n.HourlyPrice * max(share(float64(p.CPU), float64(n.CPU)), share(float64(p.Memory), float64(n.Memory)))
	return &c
}

func containerCost(c Container, n Node) *float64 {
	return PodCost(Pod{CPU: c.CPU, Memory: c.Memory}, n)
}
