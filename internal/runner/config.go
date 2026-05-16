package runner

import "github.com/s1liconcow/skiff/internal/config"

func ParseUserData(body []byte) (config.Config, error) {
	return config.ParseRunnerUserData(body)
}

func ValidateConfig(cfg config.Config) error {
	return config.Validate(config.Loaded{
		Config: cfg,
		Sources: map[string]string{
			config.FieldMode:        "runner",
			config.FieldEnv:         "runner",
			config.FieldProvider:    "runner",
			config.FieldRegion:      "runner",
			config.FieldStateBucket: "runner",
			config.FieldService:     "runner",
			config.FieldControlKey:  "runner",
		},
	})
}
