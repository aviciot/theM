"""
Demo: Python app asks Claude for a PDF file.
Shows exactly what comes back in the API response.

Usage:
  Direct to Anthropic:  python ask_for_pdf.py
  Via the-M gateway:    python ask_for_pdf.py --via-them
"""

import argparse
import json
from anthropic import Anthropic

# the-M gateway config
THEM_BASE_URL = "http://10.55.125.43:8088/payops_ai/llm/v1"
THEM_TOKEN    = "0556f47164aba4aa0ef77b29067e034b4ea57ade6050ce5e3c49f8fd65f3ad50"

MODEL = "claude-sonnet-4-6"

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--via-them", action="store_true", help="Route through the-M gateway")
    args = parser.parse_args()

    if args.via_them:
        print("=== Routing through the-M gateway ===")
        client = Anthropic(base_url=THEM_BASE_URL, api_key=THEM_TOKEN)
    else:
        print("=== Routing directly to Anthropic ===")
        client = Anthropic()  # uses ANTHROPIC_API_KEY env var

    message = client.messages.create(
        model=MODEL,
        max_tokens=1024,
        messages=[
            {
                "role": "user",
                "content": "Please generate a simple PDF file with the text 'Hello World'. Return the file content so I can save it."
            }
        ]
    )

    print("\n--- Raw response content ---")
    for block in message.content:
        print(f"Type: {block.type}")
        text = block.text if hasattr(block, "text") else str(block)
        print(f"Content (first 500 chars):\n{text[:500]}")
        print(f"\nTotal length: {len(text)} chars")

        # Check if it looks like base64 binary content
        import base64, re
        b64_matches = re.findall(r'[A-Za-z0-9+/]{100,}={0,2}', text)
        if b64_matches:
            print(f"\n>>> Found {len(b64_matches)} potential base64 block(s)")
            for m in b64_matches[:2]:
                try:
                    decoded = base64.b64decode(m)
                    print(f"    Decoded {len(decoded)} bytes — first bytes: {decoded[:8].hex()}")
                except Exception:
                    print(f"    Not valid base64")
        else:
            print("\n>>> No base64 binary content detected — LLM returned plain text only")

if __name__ == "__main__":
    main()
