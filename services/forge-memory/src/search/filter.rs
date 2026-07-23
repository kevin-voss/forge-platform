//! Structured metadata filter evaluation (equality and `$in`).

use serde_json::Value;

/// Return true when `metadata` satisfies every predicate in `filter`.
///
/// Filter shape (all keys AND-ed):
/// * `"key": <scalar|array|object>` — equality against `metadata.key`
/// * `"key": { "$in": [ ... ] }` — `metadata.key` must equal one list element
///
/// Missing / null / empty object filter matches everything. Non-object filter
/// values are ignored (treated as match-all) so callers can pass `null`.
pub fn matches_filter(filter: Option<&Value>, metadata: &Value) -> bool {
    let Some(filter) = filter else {
        return true;
    };
    if filter.is_null() {
        return true;
    }
    let Some(obj) = filter.as_object() else {
        return true;
    };
    if obj.is_empty() {
        return true;
    }
    for (key, pred) in obj {
        let meta_val = metadata.get(key).unwrap_or(&Value::Null);
        if !predicate_matches(pred, meta_val) {
            return false;
        }
    }
    true
}

fn predicate_matches(pred: &Value, meta_val: &Value) -> bool {
    if let Some(pred_obj) = pred.as_object() {
        if pred_obj.len() == 1 {
            if let Some(list) = pred_obj.get("$in") {
                let Some(arr) = list.as_array() else {
                    return false;
                };
                return arr.iter().any(|v| v == meta_val);
            }
        }
        // Object predicate without sole `$in` → structural equality.
        return meta_val == pred;
    }
    meta_val == pred
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn equality_includes_and_excludes() {
        let meta = json!({"type": "deploy", "sev": 1});
        assert!(matches_filter(Some(&json!({"type": "deploy"})), &meta));
        assert!(!matches_filter(Some(&json!({"type": "alert"})), &meta));
        assert!(matches_filter(
            Some(&json!({"type": "deploy", "sev": 1})),
            &meta
        ));
        assert!(!matches_filter(
            Some(&json!({"type": "deploy", "sev": 2})),
            &meta
        ));
    }

    #[test]
    fn in_predicate() {
        let meta = json!({"type": "deploy"});
        assert!(matches_filter(
            Some(&json!({"type": {"$in": ["deploy", "alert"]}})),
            &meta
        ));
        assert!(!matches_filter(
            Some(&json!({"type": {"$in": ["alert", "page"]}})),
            &meta
        ));
    }

    #[test]
    fn empty_filter_matches() {
        let meta = json!({"type": "deploy"});
        assert!(matches_filter(None, &meta));
        assert!(matches_filter(Some(&json!({})), &meta));
        assert!(matches_filter(Some(&Value::Null), &meta));
    }
}
