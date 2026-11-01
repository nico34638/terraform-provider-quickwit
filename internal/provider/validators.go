package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// funcValidator rejects string values for which check returns an error, with
// that error as the diagnostic detail.
type funcValidator struct {
	desc  string
	check func(string) error
}

var _ validator.String = funcValidator{}

func (v funcValidator) Description(context.Context) string         { return v.desc }
func (v funcValidator) MarkdownDescription(context.Context) string { return v.desc }

func (v funcValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := v.check(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", err.Error())
	}
}
