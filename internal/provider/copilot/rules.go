package copilot

import "github.com/ya222/defib/internal/detect"

// DetectionRules returns Copilot's built-in rules. Only a low-priority SUCCESS
// rule is shipped: Copilot's rate-limit/quota output has not been captured, so
// (per docs/detection.md) failure rules are deferred until real fixtures exist.
func (*Copilot) DetectionRules() []detect.Rule {
	return []detect.Rule{
		{
			Name:     "copilot.success",
			Category: detect.CategorySuccess,
			Priority: 1,
			Match:    detect.Match{ExitCodeIn: []int{0}},
		},
	}
}
