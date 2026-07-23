//! Naming rules for collections and record ids.

/// Collection names: 1–128 chars, `a-z0-9_-`, must start/end alphanumeric.
pub fn validate_collection_name(name: &str) -> Result<(), String> {
    let name = name.trim();
    if name.is_empty() {
        return Err("collection name is required".into());
    }
    if name.len() > 128 {
        return Err("collection name must be at most 128 characters".into());
    }
    if name.contains('\0') {
        return Err("collection name must not contain NUL".into());
    }
    if name.contains('/') || name.contains('\\') {
        return Err("collection name must not contain path separators".into());
    }
    if name.contains("..") {
        return Err("collection name must not contain '..'".into());
    }
    if name == "meta" || name == "vectors" {
        return Err(format!("collection name {name:?} is reserved"));
    }
    let bytes = name.as_bytes();
    if !bytes[0].is_ascii_alphanumeric() || !bytes[bytes.len() - 1].is_ascii_alphanumeric() {
        return Err("collection name must start and end with a letter or digit".into());
    }
    if !name
        .chars()
        .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-' || c == '_')
    {
        return Err("collection name may only contain a-z, 0-9, '-', and '_'".into());
    }
    Ok(())
}

/// Record ids: 1–256 chars; no NUL, no path separators, no `..`.
pub fn validate_record_id(id: &str) -> Result<(), String> {
    let id = id.trim();
    if id.is_empty() {
        return Err("record id is required".into());
    }
    if id.len() > 256 {
        return Err("record id must be at most 256 characters".into());
    }
    if id.contains('\0') {
        return Err("record id must not contain NUL".into());
    }
    if id.contains('/') || id.contains('\\') {
        return Err("record id must not contain path separators".into());
    }
    if id.contains("..") {
        return Err("record id must not contain '..'".into());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn collection_accepts_valid() {
        assert!(validate_collection_name("incidents").is_ok());
        assert!(validate_collection_name("a1").is_ok());
        assert!(validate_collection_name("my_collection-1").is_ok());
    }

    #[test]
    fn collection_rejects_bad() {
        assert!(validate_collection_name("").is_err());
        assert!(validate_collection_name("../etc").is_err());
        assert!(validate_collection_name("HasUpper").is_err());
        assert!(validate_collection_name("-leading").is_err());
        assert!(validate_collection_name("meta").is_err());
    }

    #[test]
    fn record_id_rejects_traversal() {
        assert!(validate_record_id("rec-1").is_ok());
        assert!(validate_record_id("../x").is_err());
        assert!(validate_record_id("a/b").is_err());
        assert!(validate_record_id("").is_err());
    }
}
