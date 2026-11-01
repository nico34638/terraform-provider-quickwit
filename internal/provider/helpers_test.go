package provider

import (
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func endpointFromEnv() string {
	if e := os.Getenv("QUICKWIT_ENDPOINT"); e != "" {
		return e
	}
	return defaultEndpoint
}

func tfjsonPath(root string, keys ...string) tfjsonpath.Path {
	p := tfjsonpath.New(root)
	for _, k := range keys {
		p = p.AtMapKey(k)
	}
	return p
}

func attrValue(s *terraform.State, name, key string) (string, error) {
	rs, ok := s.RootModule().Resources[name]
	if !ok {
		return "", fmt.Errorf("resource %s not found", name)
	}
	return rs.Primary.Attributes[key], nil
}

// storeAttr saves an attribute value for comparison in later steps.
func storeAttr(name, key string, dst *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		v, err := attrValue(s, name, key)
		*dst = v
		return err
	}
}

func checkAttrEquals(name, key string, want *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		v, err := attrValue(s, name, key)
		if err == nil && v != *want {
			err = fmt.Errorf("%s.%s = %q, want %q", name, key, v, *want)
		}
		return err
	}
}

func checkAttrDiffers(name, key string, old *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		v, err := attrValue(s, name, key)
		if err == nil && v == *old {
			err = fmt.Errorf("%s.%s should have changed from %q", name, key, *old)
		}
		*old = v
		return err
	}
}

// knownString checks a plan value against a string captured by an earlier step.
func knownString(want *string) knownvalue.Check { return stringPtrCheck{want} }

type stringPtrCheck struct{ want *string }

func (c stringPtrCheck) CheckValue(v any) error {
	if s, ok := v.(string); !ok || s != *c.want {
		return fmt.Errorf("expected %q, got %v", *c.want, v)
	}
	return nil
}

func (c stringPtrCheck) String() string { return *c.want }
