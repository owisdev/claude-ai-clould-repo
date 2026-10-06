package search

import "testing"

func TestRelevant(t *testing.T) {
	tests := []struct {
		query string
		texts []string
		want  bool
	}{
		// From the local test: AliExpress SSK results are relevant.
		{"SSK ssd m3 enclosure", []string{"SSK 20Gbps NVMe SSD Enclosure Aluminum Case - AliExpress"}, true},
		// SHEIN pages returned for an SSD search are not (Arabic title,
		// English link slug).
		{"SSK ssd m3 enclosure", []string{"Shein قميص فتاة صغيرة بطيات",
			"/SHEIN-Young-Girl-Ruffle-Trim-Tee-Sunflower-Print-Belted-Shorts-p-12821272.html"}, false},
		// The link slug counts, so an Arabic title can still match.
		{"phone case", []string{"جراب هاتف", "/Clear-Phone-Case-p-123456.html"}, true},
		// Plurals and case.
		{"usb hub", []string{"USB Hubs for laptops"}, true},
		// Two thirds of the words are needed: one typo in four words is
		// tolerated, a different product sharing the brand is not.
		{"SSK ssd m3 enclosure", []string{"SSK M.2 NVME SSD Enclosure Adapter"}, true},
		{"SSK ssd m3 enclosure", []string{"SSK Portable SSD USB Drive 550MB/S External Solid State Drive"}, false},
		{"samsung s pen", []string{"Samsung Galaxy S Pen Pro"}, true},
		{"samsung s pen", []string{"Galaxy S Pen Pro"}, false},
		{"samsung galaxy s24 ultra case", []string{"Samsung Galaxy S24 Ultra Clear Case"}, true},
		{"samsung galaxy s24 ultra case", []string{"Samsung Galaxy S24 charger"}, false},
		// Arabic queries.
		{"سماعة بلوتوث", []string{"سماعة رأس بلوتوث لاسلكية"}, true},
		// Nothing usable to compare.
		{"x", []string{"anything"}, true},
	}
	for _, tt := range tests {
		if got := Relevant(tt.query, tt.texts...); got != tt.want {
			t.Errorf("Relevant(%q, %q) = %v, want %v", tt.query, tt.texts, got, tt.want)
		}
	}
}
