package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/brunoga/deep/v3"
)

// --- CONFIGURATION SCHEMA ---

// SystemConfig is the root configuration for our entire infrastructure.
type SystemConfig struct {
	Version        int             `deep:"readonly" json:"version"` // Managed by server
	Environment    string          `json:"environment"`
	Server         ServerConfig    `json:"server"`
	FeatureToggles map[string]bool `json:"feature_toggles"`
	// Integrations is a keyed list. We use "Name" as the unique identifier.
	Integrations []Integration `deep:"key" json:"integrations"`
}

type ServerConfig struct {
	Host    string `json:"host"`
	Port    int    `deep:"atomic" json:"port"` // Port must be updated as a whole unit
	Timeout int    `json:"timeout"`
}

type Integration struct {
	Name    string `deep:"key" json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// --- CONFIGURATION MANAGER ---

// ConfigManager handles the live state, history, and validation of configurations.
type ConfigManager struct {
	mu sync.RWMutex

	// live is the current active configuration.
	live *SystemConfig

	// history stores the patches used to transition between versions.
	// history[0] is the patch from v0 to v1, and so on.
	history []deep.Patch[SystemConfig]
}

func NewConfigManager(initial SystemConfig) *ConfigManager {
	initial.Version = 1
	return &ConfigManager{
		live: &initial,
	}
}

// Update attempts to transition the live configuration to a new state.
func (m *ConfigManager) Update(newConfig SystemConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Prepare the update.
	// We increment the version automatically.
	newConfig.Version = m.live.Version + 1

	// 2. Generate the patch.
	patch := deep.MustDiff(*m.live, newConfig)
	if patch == nil {
		return fmt.Errorf("no changes detected")
	}

	// 3. Define Validation Rules.
	// In a real app, these might be loaded from a policy engine.
	// Rule A: Timeout must not exceed 60 seconds.
	// Rule B: Port must be in the "safe" range ( > 1024).
	builder := deep.NewPatchBuilder[SystemConfig]()
	builder.AddCondition("/Server/Timeout <= 60")
	builder.AddCondition("/Server/Port > 1024")

	validationRules, err := builder.Build()
	if err != nil {
		return fmt.Errorf("failed to build validation rules: %w", err)
	}

	// We combine the user patch with our validation rules.
	// Note: In this architecture, we apply the patch to a COPY first to validate.
	testCopy := deep.MustCopy(*m.live)
	patch.Apply(&testCopy)

	// Check validation rules.
	if validationRules != nil {
		if err := validationRules.ApplyChecked(&testCopy); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
	}

	// 4. Generate Audit Log before applying.
	fmt.Printf("\n[Version %d] PROPOSING CHANGES:\n", newConfig.Version)
	_ = patch.Walk(func(path string, op deep.OpKind, old, new any) error {
		fmt.Printf("  - %s %s: %v -> %v\n", strings.ToUpper(op.String()), path, old, new)
		return nil
	})

	// 5. Apply the patch to live state.
	patch.Apply(m.live)
	m.live.Version = newConfig.Version

	// 6. Record history for rollback capability.
	m.history = append(m.history, patch)

	fmt.Printf("[Version %d] System state synchronized successfully.\n", m.live.Version)
	return nil
}

// Rollback reverts the live configuration to the previous version.
func (m *ConfigManager) Rollback() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.history) == 0 {
		return fmt.Errorf("no history to rollback")
	}

	// 1. Get the last patch.
	lastPatch := m.history[len(m.history)-1]

	// 2. Reverse it.
	undoPatch := lastPatch.Reverse()

	// 3. Apply it to live state.
	undoPatch.Apply(m.live)
	m.live.Version--

	// 4. Clean up history.
	m.history = m.history[:len(m.history)-1]

	fmt.Printf("\n[ROLLBACK] System reverted to Version %d.\n", m.live.Version)
	return nil
}

func (m *ConfigManager) Current() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, err := json.MarshalIndent(m.live, "", "  ")
	if err != nil {
		fmt.Printf("State serialization failed: %v\n", err)
		return ""
	}
	return string(data)
}

// --- MAIN APPLICATION LOOP ---

func main() {
	fmt.Println("=== CLOUD CONFIGURATION MANAGER STARTING ===")

	// 1. Boot system with initial configuration.
	manager := NewConfigManager(SystemConfig{
		Environment: "production",
		Server: ServerConfig{
			Host:    "api.prod.local",
			Port:    8080,
			Timeout: 30,
		},
		FeatureToggles: map[string]bool{
			"billing_v2": false,
		},
		Integrations: []Integration{
			{Name: "S3", URL: "https://s3.aws.com", Enabled: true},
		},
	})

	fmt.Println("Initial Configuration (v1):")
	fmt.Println(manager.Current())

	// 2. Scenario: Valid Update (Adding a role and changing timeout).
	update1 := deep.MustCopy(*manager.live)
	update1.Server.Timeout = 45
	update1.FeatureToggles["billing_v2"] = true
	update1.Integrations = append(update1.Integrations, Integration{
		Name: "Stripe", URL: "https://api.stripe.com", Enabled: true,
	})

	if err := manager.Update(update1); err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// 3. Scenario: REJECTED Update (Violating business rules).
	update2 := deep.MustCopy(*manager.live)
	update2.Server.Timeout = 120 // TOO HIGH! (Max is 60)
	update2.Server.Port = 80     // TOO LOW! (Min is 1024)

	fmt.Println("\n--- ATTEMPTING INVALID UPDATE (Timeout=120, Port=80) ---")
	if err := manager.Update(update2); err != nil {
		fmt.Printf("REJECTED: %v\n", err)
	}

	// 4. Scenario: Complex reordering and modification of integrations.
	update3 := deep.MustCopy(*manager.live)
	// Swap order of Stripe and S3, and update Stripe URL.
	update3.Integrations = []Integration{
		update3.Integrations[1], // Stripe
		update3.Integrations[0], // S3
	}
	update3.Integrations[0].URL = "https://stripe.com/v3"

	if err := manager.Update(update3); err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// 5. Final state check.
	fmt.Println("\nFinal Configuration State:")
	fmt.Println(manager.Current())

	// 6. Demonstrate Rollback.
	_ = manager.Rollback()
	fmt.Println("\nConfiguration State after Rollback:")
	fmt.Println(manager.Current())
}
