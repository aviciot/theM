package pii_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aviciot/them/internal/middleware"
	"github.com/aviciot/them/internal/middleware/pii"
)

func TestZZZDebugRealConfig(t *testing.T) {
	d := pii.New()
	cfg, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true, Mode: "warn", Direction: "both", Categories: []string{"email"}})
	fmt.Println("cfg json:", string(cfg))
	r, err := d.Process(context.Background(), middleware.Part{Kind: "text", Text: "My billing is wrong, please contact me at jane.doe@example.com"}, cfg)
	fmt.Printf("result: %+v err: %v\n", r, err)
}
