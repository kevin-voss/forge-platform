//! Map (method, path) → secret/config authorization action.

/// Platform authz actions honored by Identity's permission matrix.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AuthAction {
    SecretRead,
    SecretWrite,
    ConfigRead,
    ConfigWrite,
}

impl AuthAction {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::SecretRead => "secret.read",
            Self::SecretWrite => "secret.write",
            Self::ConfigRead => "config.read",
            Self::ConfigWrite => "config.write",
        }
    }

    /// True for secret writes and plaintext reveals — fail-closed without Identity/cache.
    pub fn is_write_or_reveal(self, path: &str) -> bool {
        match self {
            Self::SecretWrite | Self::ConfigWrite => true,
            Self::SecretRead => path.contains(":access") || path.ends_with("/resolve"),
            Self::ConfigRead => false,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum AuthTarget {
    /// Health / identity / data-keys — middleware skips.
    Skip,
    /// Authenticate + authorize `action` on path `project_id`.
    Authorize {
        action: AuthAction,
        project_id: String,
    },
}

/// Resolve authorization target for a Secrets HTTP request.
pub fn map_action(method: &str, path: &str) -> AuthTarget {
    let m = method.to_ascii_uppercase();
    let trimmed = path.trim_end_matches('/');
    let p = if trimmed.is_empty() { "/" } else { trimmed };

    if p.starts_with("/health") || p == "/" {
        return AuthTarget::Skip;
    }
    // Data-key bootstrap remains unauthenticated (internal/setup; 10.01/10.02).
    if p.contains("/data-keys") {
        return AuthTarget::Skip;
    }

    // /v1/projects/{pid}/envs/{env}/secrets[/{raw}]
    if let Some(rest) = p.strip_prefix("/v1/projects/") {
        let mut parts = rest.split('/');
        let Some(project_id) = parts.next().filter(|s| !s.is_empty()) else {
            return AuthTarget::Skip;
        };
        if parts.next() != Some("envs") {
            return AuthTarget::Skip;
        }
        if parts.next().filter(|s| !s.is_empty()).is_none() {
            return AuthTarget::Skip;
        }
        let Some(kind) = parts.next() else {
            return AuthTarget::Skip;
        };
        let name = parts.next();

        // /v1/projects/{pid}/envs/{env}/services/{svc}/bindings|resolve
        if kind == "services" {
            let _service = name;
            let Some(op) = parts.next() else {
                return AuthTarget::Skip;
            };
            return match (op, m.as_str()) {
                ("bindings", "GET") => AuthTarget::Authorize {
                    action: AuthAction::SecretRead,
                    project_id: project_id.to_string(),
                },
                ("bindings", "PUT") => AuthTarget::Authorize {
                    action: AuthAction::SecretWrite,
                    project_id: project_id.to_string(),
                },
                ("resolve", "POST") => AuthTarget::Authorize {
                    action: AuthAction::SecretRead,
                    project_id: project_id.to_string(),
                },
                _ => AuthTarget::Skip,
            };
        }

        match (kind, m.as_str(), name) {
            ("secrets", "GET", None) => AuthTarget::Authorize {
                action: AuthAction::SecretRead,
                project_id: project_id.to_string(),
            },
            ("secrets", "GET", Some(_)) => AuthTarget::Authorize {
                action: AuthAction::SecretRead,
                project_id: project_id.to_string(),
            },
            ("secrets", "PUT", Some(_)) => AuthTarget::Authorize {
                action: AuthAction::SecretWrite,
                project_id: project_id.to_string(),
            },
            ("secrets", "POST", Some(raw)) if raw.ends_with(":access") => AuthTarget::Authorize {
                action: AuthAction::SecretRead,
                project_id: project_id.to_string(),
            },
            ("secrets", "POST", Some(_)) => AuthTarget::Authorize {
                action: AuthAction::SecretRead,
                project_id: project_id.to_string(),
            },
            ("config", "GET", None) => AuthTarget::Authorize {
                action: AuthAction::ConfigRead,
                project_id: project_id.to_string(),
            },
            ("config", "GET", Some(_)) => AuthTarget::Authorize {
                action: AuthAction::ConfigRead,
                project_id: project_id.to_string(),
            },
            ("config", "PUT", Some(_)) => AuthTarget::Authorize {
                action: AuthAction::ConfigWrite,
                project_id: project_id.to_string(),
            },
            ("config", "DELETE", Some(_)) => AuthTarget::Authorize {
                action: AuthAction::ConfigWrite,
                project_id: project_id.to_string(),
            },
            _ => AuthTarget::Skip,
        }
    } else {
        AuthTarget::Skip
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maps_config_get_to_config_read() {
        let t = map_action("GET", "/v1/projects/prj_1/envs/production/config");
        match t {
            AuthTarget::Authorize { action, project_id } => {
                assert_eq!(action, AuthAction::ConfigRead);
                assert_eq!(action.as_str(), "config.read");
                assert_eq!(project_id, "prj_1");
            }
            other => panic!("unexpected {other:?}"),
        }
    }

    #[test]
    fn maps_secret_access_to_secret_read() {
        let t = map_action(
            "POST",
            "/v1/projects/prj_1/envs/production/secrets/DATABASE_PASSWORD:access",
        );
        match t {
            AuthTarget::Authorize { action, project_id } => {
                assert_eq!(action, AuthAction::SecretRead);
                assert_eq!(action.as_str(), "secret.read");
                assert_eq!(project_id, "prj_1");
                assert!(action.is_write_or_reveal(
                    "/v1/projects/prj_1/envs/production/secrets/DATABASE_PASSWORD:access"
                ));
            }
            other => panic!("unexpected {other:?}"),
        }
    }

    #[test]
    fn maps_put_secret_to_secret_write() {
        let t = map_action(
            "PUT",
            "/v1/projects/prj_1/envs/production/secrets/DATABASE_PASSWORD",
        );
        match t {
            AuthTarget::Authorize { action, .. } => {
                assert_eq!(action, AuthAction::SecretWrite);
                assert_eq!(action.as_str(), "secret.write");
            }
            other => panic!("unexpected {other:?}"),
        }
    }

    #[test]
    fn maps_put_delete_config_to_config_write() {
        let put = map_action("PUT", "/v1/projects/prj_a/envs/e/config/FEATURE_X");
        let del = map_action("DELETE", "/v1/projects/prj_a/envs/e/config/FEATURE_X");
        assert!(matches!(
            put,
            AuthTarget::Authorize {
                action: AuthAction::ConfigWrite,
                ..
            }
        ));
        assert!(matches!(
            del,
            AuthTarget::Authorize {
                action: AuthAction::ConfigWrite,
                ..
            }
        ));
    }

    #[test]
    fn health_and_data_keys_skip() {
        assert_eq!(map_action("GET", "/health/live"), AuthTarget::Skip);
        assert_eq!(
            map_action("POST", "/v1/projects/p/data-keys"),
            AuthTarget::Skip
        );
    }

    #[test]
    fn maps_bindings_and_resolve() {
        let put = map_action(
            "PUT",
            "/v1/projects/prj_1/envs/production/services/demo/bindings",
        );
        assert!(matches!(
            put,
            AuthTarget::Authorize {
                action: AuthAction::SecretWrite,
                ..
            }
        ));
        let resolve = map_action(
            "POST",
            "/v1/projects/prj_1/envs/production/services/demo/resolve",
        );
        match resolve {
            AuthTarget::Authorize { action, project_id } => {
                assert_eq!(action, AuthAction::SecretRead);
                assert_eq!(project_id, "prj_1");
                assert!(action.is_write_or_reveal(
                    "/v1/projects/prj_1/envs/production/services/demo/resolve"
                ));
            }
            other => panic!("unexpected {other:?}"),
        }
    }
}
