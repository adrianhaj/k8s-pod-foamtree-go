package foam

import (
	"strings"
	"testing"
)

const priceCSV = `# list prices, USD per hour
instance_type,region,capacity_type,hourly_usd
m5.xlarge,,,0.192
m5.xlarge,eu-west-1,,0.214
m5.xlarge,eu-west-1,spot,0.07
,,,0.05
`

func TestParsePrices(t *testing.T) {
	p, err := ParsePrices(strings.NewReader(priceCSV))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		node Node
		want float64
	}{
		{"type anywhere", Node{InstanceType: "m5.xlarge", Region: "us-east-1"}, 0.192},
		{"region beats type alone", Node{InstanceType: "m5.xlarge", Region: "eu-west-1", CapacityType: "on-demand"}, 0.214},
		{"spot beats region", Node{InstanceType: "m5.xlarge", Region: "eu-west-1", CapacityType: "spot"}, 0.07},
		{"catch-all", Node{InstanceType: "c5.large"}, 0.05},
		{"no labels at all", Node{}, 0.05},
	}
	for _, tc := range cases {
		if got, ok := p.Price(tc.node); !ok || got != tc.want {
			t.Errorf("%s: got %v %v, want %v", tc.name, got, ok, tc.want)
		}
	}
	if _, ok := p[:len(p)-1].Price(Node{InstanceType: "c5.large"}); ok {
		t.Error("unlisted type priced without a catch-all")
	}
}

func TestParsePricesFirstRowWinsTies(t *testing.T) {
	p, err := ParsePrices(strings.NewReader("instance_type,region,capacity_type,hourly_usd\na,,,1\na,,,2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Price(Node{InstanceType: "a"}); got != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestPriceRanking(t *testing.T) {
	p, err := ParsePrices(strings.NewReader("\ufeffinstance_type,region,capacity_type,hourly_usd\n" +
		",eu-west-1,spot,0.05\n g5.12xlarge , ,,5.67\nm5.xlarge,,on-demand,0.19\n"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		node Node
		want float64
		ok   bool
	}{
		{"instance type beats region and spot", Node{InstanceType: "g5.12xlarge", Region: "eu-west-1", CapacityType: "spot"}, 5.67, true},
		{"region and spot without a type row", Node{InstanceType: "c5.large", Region: "eu-west-1", CapacityType: "spot"}, 0.05, true},
		{"on-demand matches an unlabelled node", Node{InstanceType: "m5.xlarge"}, 0.19, true},
		{"on-demand never matches spot", Node{InstanceType: "m5.xlarge", CapacityType: "spot"}, 0, false},
	}
	for _, tc := range cases {
		if got, ok := p.Price(tc.node); ok != tc.ok || got != tc.want {
			t.Errorf("%s: got %v %v, want %v %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParsePricesErrorLine(t *testing.T) {
	_, err := ParsePrices(strings.NewReader("# prices\ninstance_type,region,capacity_type,hourly_usd\n# m5\nm5.xlarge,,,free\n"))
	if err == nil || !strings.HasPrefix(err.Error(), "line 4:") {
		t.Fatalf("got %v", err)
	}
}

func TestParsePricesRejects(t *testing.T) {
	const header = "instance_type,region,capacity_type,hourly_usd\n"
	for name, in := range map[string]string{
		"empty":           "",
		"wrong header":    "type,region,capacity,price\na,,,1\n",
		"no rows":         header,
		"negative":        header + "a,,,-1\n",
		"not a number":    header + "a,,,cheap\n",
		"infinite":        header + "a,,,Inf\n",
		"NaN":             header + "a,,,NaN\n",
		"capacity typo":   header + "a,,spto,1\n",
		"too few columns": header + "a,,1\n",
	} {
		if _, err := ParsePrices(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPodCost(t *testing.T) {
	price := 1.0
	n := Node{CPU: 4000, Memory: 16_000_000_000, HourlyPrice: &price}
	cases := []struct {
		name string
		pod  Pod
		want float64
	}{
		{"cpu dominant", Pod{CPU: 2000, Memory: 1_600_000_000}, 0.5},
		{"memory dominant", Pod{CPU: 400, Memory: 4_000_000_000}, 0.25},
		{"requests nothing", Pod{}, 0},
	}
	for _, tc := range cases {
		if got := PodCost(tc.pod, n); got == nil || *got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := PodCost(Pod{CPU: 2000}, Node{CPU: 4000}); got != nil {
		t.Errorf("unpriced node: got %v", *got)
	}
}
