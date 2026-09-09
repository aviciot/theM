package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/temporal/workerconfig"
)

func TestResolveProvider_AppKeyUsedWhenSet(t *testing.T) {
	f := &runOrchestratorFactory{}
	cfg := workerconfig.RunConfig{
		LLMProvider: "anthropic",
		LLMAPIKey:   "sk-ant-app-key",
		OrchestratorConfig: orchestrator.Config{
			Model: "claude-sonnet-4-6",
		},
	}
	p, err := f.resolveProvider(cfg)
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestResolveProvider_EnvKeyUsedWhenAppKeyEmpty(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-env-key")
	f := &runOrchestratorFactory{}
	cfg := workerconfig.RunConfig{
		LLMProvider: "anthropic",
		LLMAPIKey:   "", // no per-app key
		OrchestratorConfig: orchestrator.Config{
			Model: "claude-sonnet-4-6",
		},
	}
	p, err := f.resolveProvider(cfg)
	require.NoError(t, err)
	assert.NotNil(t, p, "should fall back to env key when app key is empty")
}

func TestResolveProvider_FailsWhenNeitherKeySet(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	f := &runOrchestratorFactory{}
	cfg := workerconfig.RunConfig{
		LLMProvider: "anthropic",
		LLMAPIKey:   "",
		OrchestratorConfig: orchestrator.Config{
			Model: "claude-sonnet-4-6",
		},
	}
	_, err := f.resolveProvider(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no API key configured")
}

func TestResolveProvider_UnsupportedProviderFails(t *testing.T) {
	f := &runOrchestratorFactory{}
	cfg := workerconfig.RunConfig{
		LLMProvider: "vertex",
		LLMAPIKey:   "some-key",
		OrchestratorConfig: orchestrator.Config{},
	}
	_, err := f.resolveProvider(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}
