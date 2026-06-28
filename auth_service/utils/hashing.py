"""
Hashing Utilities
=================
Functions for hashing API keys and tokens.
"""

import hashlib
import secrets


def hash_api_key(api_key: str) -> str:
    """
    Hash API key with SHA256.

    Args:
        api_key: Raw API key

    Returns:
        str: SHA256 hash of API key
    """
    return hashlib.sha256(api_key.encode()).hexdigest()


def hash_token(token: str) -> str:
    """
    Hash token for database storage.

    Args:
        token: Raw token (JWT or refresh token)

    Returns:
        str: SHA256 hash of token
    """
    return hashlib.sha256(token.encode()).hexdigest()


def generate_api_key() -> str:
    """
    Generate secure API key.

    Returns:
        str: New API key with 'ak_' prefix
    """
    return f"ak_{secrets.token_urlsafe(32)}"
