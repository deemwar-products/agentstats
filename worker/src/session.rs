//! HMAC-signed session cookie: `github_id|exp`, signed with SESSION_KEY (a
//! Worker secret). No token or PII in the cookie. Cookie attrs: HttpOnly,
//! Secure, SameSite=Lax, ~30 days.

use hmac::{Hmac, Mac};
use sha2::Sha256;

type HmacSha256 = Hmac<Sha256>;

const MAX_AGE_SECS: i64 = 60 * 60 * 24 * 30; // 30 days
pub const COOKIE_NAME: &str = "as_session";

/// Sign `github_id|exp` → `github_id|exp|hexmac`.
pub fn issue(github_id: i64, key: &[u8], now_secs: i64) -> String {
    let exp = now_secs + MAX_AGE_SECS;
    let payload = format!("{github_id}|{exp}");
    let mac = sign(&payload, key);
    format!("{payload}|{mac}")
}

/// Verify a cookie value and return the github_id if the signature is valid and
/// not expired. Constant-time MAC comparison via the `hmac` crate's `verify`.
pub fn verify(value: &str, key: &[u8], now_secs: i64) -> Option<i64> {
    let parts: Vec<&str> = value.split('|').collect();
    if parts.len() != 3 {
        return None;
    }
    let (id_s, exp_s, mac_hex) = (parts[0], parts[1], parts[2]);
    let payload = format!("{id_s}|{exp_s}");
    let expected = hex::decode(mac_hex).ok()?;
    let mut m = HmacSha256::new_from_slice(key).ok()?;
    m.update(payload.as_bytes());
    m.verify_slice(&expected).ok()?;
    let exp: i64 = exp_s.parse().ok()?;
    if exp < now_secs {
        return None;
    }
    id_s.parse().ok()
}

fn sign(payload: &str, key: &[u8]) -> String {
    let mut m = HmacSha256::new_from_slice(key).expect("HMAC accepts any key length");
    m.update(payload.as_bytes());
    hex::encode(m.finalize().into_bytes())
}

/// Build the Set-Cookie header value.
pub fn set_cookie_header(value: &str) -> String {
    format!(
        "{COOKIE_NAME}={value}; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age={MAX_AGE_SECS}"
    )
}

/// Expire the cookie (logout).
pub fn clear_cookie_header() -> String {
    format!("{COOKIE_NAME}=; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=0")
}

/// Pull our cookie value out of a raw `Cookie:` header.
pub fn read_cookie(cookie_header: &str) -> Option<String> {
    for pair in cookie_header.split(';') {
        let pair = pair.trim();
        if let Some(v) = pair.strip_prefix(&format!("{COOKIE_NAME}=")) {
            return Some(v.to_string());
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip() {
        let key = b"test-key-not-a-real-secret";
        let now = 1_700_000_000;
        let c = issue(42, key, now);
        assert_eq!(verify(&c, key, now), Some(42));
    }

    #[test]
    fn rejects_tamper() {
        let key = b"test-key-not-a-real-secret";
        let now = 1_700_000_000;
        let c = issue(42, key, now);
        let tampered = c.replacen("42|", "43|", 1);
        assert_eq!(verify(&tampered, key, now), None);
    }

    #[test]
    fn rejects_expired() {
        let key = b"test-key-not-a-real-secret";
        let c = issue(42, key, 1_000);
        assert_eq!(verify(&c, key, 1_000 + 60 * 60 * 24 * 31), None);
    }

    #[test]
    fn rejects_wrong_key() {
        let c = issue(42, b"key-a", 1_700_000_000);
        assert_eq!(verify(&c, b"key-b", 1_700_000_000), None);
    }
}
