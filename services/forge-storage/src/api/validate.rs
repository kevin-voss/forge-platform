//! Naming rules for buckets and object keys.

/// Bucket names: 3–63 chars, lowercase `a-z0-9` and `-`, must start/end alphanumeric.
/// Rejects path traversal, NUL, reserved names.
pub fn validate_bucket_name(name: &str) -> Result<(), String> {
    let name = name.trim();
    if name.is_empty() {
        return Err("bucket name is required".into());
    }
    if name.len() < 3 || name.len() > 63 {
        return Err("bucket name must be 3–63 characters".into());
    }
    if name.contains('\0') {
        return Err("bucket name must not contain NUL".into());
    }
    if name.contains('/') || name.contains('\\') {
        return Err("bucket name must not contain path separators".into());
    }
    if name.contains("..") {
        return Err("bucket name must not contain '..'".into());
    }
    if name.starts_with('.') || name.ends_with('.') {
        return Err("bucket name must not start or end with '.'".into());
    }
    if name == "meta" || name == "objects" {
        return Err(format!("bucket name {name:?} is reserved"));
    }
    let bytes = name.as_bytes();
    if !bytes[0].is_ascii_alphanumeric() || !bytes[bytes.len() - 1].is_ascii_alphanumeric() {
        return Err("bucket name must start and end with a letter or digit".into());
    }
    if !name
        .chars()
        .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-')
    {
        return Err("bucket name may only contain a-z, 0-9, and '-'".into());
    }
    Ok(())
}

/// Object keys: 1–1024 chars; no NUL, no leading `/`, no `..` path segments.
pub fn validate_object_key(key: &str) -> Result<(), String> {
    if key.is_empty() {
        return Err("object key is required".into());
    }
    if key.len() > 1024 {
        return Err("object key must be at most 1024 characters".into());
    }
    if key.contains('\0') {
        return Err("object key must not contain NUL".into());
    }
    if key.starts_with('/') || key.starts_with('\\') {
        return Err("object key must not start with a path separator".into());
    }
    for seg in key.split(['/', '\\']) {
        if seg == ".." {
            return Err("object key must not contain '..' path segments".into());
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bucket_accepts_valid() {
        assert!(validate_bucket_name("artifacts").is_ok());
        assert!(validate_bucket_name("a1b").is_ok());
        assert!(validate_bucket_name("my-bucket-1").is_ok());
    }

    #[test]
    fn bucket_rejects_traversal_and_bounds() {
        assert!(validate_bucket_name("ab").is_err());
        assert!(validate_bucket_name(&"a".repeat(64)).is_err());
        assert!(validate_bucket_name("../etc").is_err());
        assert!(validate_bucket_name("/abs").is_err());
        assert!(validate_bucket_name("HasUpper").is_err());
        assert!(validate_bucket_name("-leading").is_err());
        assert!(validate_bucket_name("trailing-").is_err());
        assert!(validate_bucket_name("meta").is_err());
        assert!(validate_bucket_name("bad_name").is_err());
        assert!(validate_bucket_name("a\0b").is_err());
    }

    #[test]
    fn object_key_rejects_traversal() {
        assert!(validate_object_key("ok/file.txt").is_ok());
        assert!(validate_object_key("../secret").is_err());
        assert!(validate_object_key("a/../b").is_err());
        assert!(validate_object_key("/abs").is_err());
        assert!(validate_object_key("").is_err());
        assert!(validate_object_key("x\0y").is_err());
    }
}
