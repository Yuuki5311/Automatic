package captcha

import (
	"strings"
	"testing"
)

func TestBaxiaDetectJS_NonEmpty(t *testing.T) {
	if baxiaDetectJS == "" || baxiaSliderRectJS == "" {
		t.Fatal("baxia JS helpers must not be empty")
	}
	for _, p := range []string{".baxia-dialog", "_____tmd_____", "action=captcha"} {
		if !strings.Contains(baxiaDetectJS, p) {
			t.Fatalf("detect JS missing %q", p)
		}
	}
	for _, p := range []string{"#nc_1_n1z", "btn_slide", "getBoundingClientRect"} {
		if !strings.Contains(baxiaSliderRectJS, p) {
			t.Fatalf("slider rect JS missing %q", p)
		}
	}
}
