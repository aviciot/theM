"""
Fernet symmetric encryption helpers.
Copied from Omni — key derived from settings.security.secret_key.
"""
import base64
import hashlib

from cryptography.fernet import Fernet

from app.config import settings

_ENC_PREFIX = "enc:"


def _fernet() -> Fernet:
    key = base64.urlsafe_b64encode(
        hashlib.sha256(settings.security.secret_key.encode()).digest()
    )
    return Fernet(key)


def encrypt_value(value: str) -> str:
    if not value or value.startswith(_ENC_PREFIX):
        return value
    return _ENC_PREFIX + _fernet().encrypt(value.encode()).decode()


def decrypt_value(value: str) -> str:
    if not value or not value.startswith(_ENC_PREFIX):
        return value
    try:
        return _fernet().decrypt(value[len(_ENC_PREFIX):].encode()).decode()
    except Exception:
        return ""


def key_hint(encrypted: str) -> str | None:
    plain = decrypt_value(encrypted)
    if not plain or len(plain) < 8:
        return None
    return f"{plain[:4]}{'•' * 8}{plain[-4:]}"
