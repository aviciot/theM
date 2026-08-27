// Package voice implements STT (speech-to-text) and TTS (text-to-speech) for
// voice entry points. It replaces the Python voice_service.py.
//
// Supported providers:
//   - STT: openai (whisper-1), groq (whisper-large-v3)
//   - TTS: openai (tts-1, voices: alloy/echo/fable/onyx/nova/shimmer),
//          elevenlabs (streaming, custom voice IDs)
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// Transcribe sends audio bytes to the named STT provider and returns the transcript.
// provider: "openai" | "groq"
// model: whisper model (e.g. "whisper-1", "whisper-large-v3"); empty = provider default
// apiKey: plaintext API key
// filename: original file name (used as the multipart filename)
// contentType: MIME type (e.g. "audio/webm", "audio/mp4")
func Transcribe(ctx context.Context, provider, model, apiKey string, audio []byte, filename, contentType string) (string, error) {
	switch provider {
	case "openai":
		return transcribeOpenAI(ctx, apiKey, model, audio, filename)
	case "groq":
		return transcribeGroq(ctx, apiKey, model, audio, filename)
	default:
		return "", fmt.Errorf("voice: unsupported STT provider %q", provider)
	}
}

// StreamTTS streams audio bytes from the TTS provider to w.
// provider: "openai" | "elevenlabs"
// voice: voice name (openai) or voice ID (elevenlabs)
// model: TTS model for openai (e.g. "tts-1"); ignored for elevenlabs
// Returns the Content-Type of the audio stream.
func StreamTTS(ctx context.Context, w io.Writer, provider, voice, model, apiKey, text string) (string, error) {
	switch provider {
	case "openai":
		return streamTTSOpenAI(ctx, w, apiKey, model, voice, text)
	case "elevenlabs":
		return streamTTSElevenLabs(ctx, w, apiKey, voice, text)
	default:
		return "", fmt.Errorf("voice: unsupported TTS provider %q", provider)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// OpenAI STT
// ──────────────────────────────────────────────────────────────────────────────

func transcribeOpenAI(ctx context.Context, apiKey, model string, audio []byte, filename string) (string, error) {
	if model == "" {
		model = "whisper-1"
	}
	body, ct, err := buildWhisperForm(audio, filename, model)
	if err != nil {
		return "", fmt.Errorf("voice/openai: build form: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/audio/transcriptions", body)
	if err != nil {
		return "", fmt.Errorf("voice/openai: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", ct)
	return doWhisperRequest(req)
}

// ──────────────────────────────────────────────────────────────────────────────
// Groq STT
// ──────────────────────────────────────────────────────────────────────────────

func transcribeGroq(ctx context.Context, apiKey, model string, audio []byte, filename string) (string, error) {
	if model == "" {
		model = "whisper-large-v3"
	}
	body, ct, err := buildWhisperForm(audio, filename, model)
	if err != nil {
		return "", fmt.Errorf("voice/groq: build form: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.groq.com/openai/v1/audio/transcriptions", body)
	if err != nil {
		return "", fmt.Errorf("voice/groq: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", ct)
	return doWhisperRequest(req)
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared Whisper helpers
// ──────────────────────────────────────────────────────────────────────────────

func buildWhisperForm(audio []byte, filename, model string) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := fw.Write(audio); err != nil {
		return nil, "", err
	}
	if err := mw.WriteField("model", model); err != nil {
		return nil, "", err
	}
	mw.Close()
	return &buf, mw.FormDataContentType(), nil
}

func doWhisperRequest(req *http.Request) (string, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("voice: whisper request: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("voice: whisper %s: %s", resp.Status, string(data))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("voice: whisper response parse: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// OpenAI TTS
// ──────────────────────────────────────────────────────────────────────────────

func streamTTSOpenAI(ctx context.Context, w io.Writer, apiKey, model, voice, text string) (string, error) {
	if model == "" {
		model = "tts-1"
	}
	if voice == "" {
		voice = "alloy"
	}
	payload, _ := json.Marshal(map[string]string{
		"model":           model,
		"input":           text,
		"voice":           voice,
		"response_format": "mp3",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/audio/speech", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("voice/openai-tts: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("voice/openai-tts: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("voice/openai-tts: %s: %s", resp.Status, string(data))
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return "", fmt.Errorf("voice/openai-tts: copy: %w", err)
	}
	return "audio/mpeg", nil
}

// ──────────────────────────────────────────────────────────────────────────────
// ElevenLabs TTS
// ──────────────────────────────────────────────────────────────────────────────

func streamTTSElevenLabs(ctx context.Context, w io.Writer, apiKey, voiceID, text string) (string, error) {
	if voiceID == "" {
		return "", fmt.Errorf("voice/elevenlabs: voice ID must be set")
	}
	url := "https://api.elevenlabs.io/v1/text-to-speech/" + voiceID + "/stream"
	payload, _ := json.Marshal(map[string]any{
		"text":     text,
		"model_id": "eleven_monolingual_v1",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("voice/elevenlabs: new request: %w", err)
	}
	req.Header.Set("xi-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("voice/elevenlabs: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("voice/elevenlabs: %s: %s", resp.Status, string(data))
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return "", fmt.Errorf("voice/elevenlabs: copy: %w", err)
	}
	return "audio/mpeg", nil
}
