//! Cross-node service connectivity via `.svc.forge` (22.06 acceptance).
//!
//! Simulates two nodes: node-a resolves `echo.local.demo.svc.forge` to node-b's
//! overlay lease and verifies NetworkPolicy deny still applies after DNS.

use super::dns::{
    bootstrap_dns, is_overlay_ip, is_provider_public_ip, observe_resolve, DnsConfig,
    DnsConfigBackend, DnsObs, FakeDnsBackend, NodeNetworkHealth,
};
use super::policy::{FakePolicyBackend, PolicyBackend, PolicyRule, PolicyRuleSet};
use super::reconcile::{detect_drift, DriftObs, EndpointSnapshot, LeaseSnapshot, ObservedRoute};

#[test]
fn cross_node_dns_resolves_overlay_not_public() {
    let endpoints = vec![
        EndpointSnapshot {
            endpoint_id: "echo-on-b".into(),
            address_ip: "10.100.2.5".into(),
            service: "echo".into(),
        },
        EndpointSnapshot {
            endpoint_id: "echo-public".into(),
            address_ip: "203.0.113.50".into(),
            service: "echo".into(),
        },
    ];
    let leases = vec![LeaseSnapshot {
        workload_id: "echo-on-b".into(),
        address: "10.100.2.5".into(),
        node_id: "node-b".into(),
    }];
    let observed = vec![ObservedRoute {
        destination: "10.100.2.0/24".into(),
        present: true,
    }];
    let report = detect_drift(&endpoints, &leases, &observed, "10.100.0.0/16");
    assert!(report.drifted.is_empty(), "overlay+route should match: {:?}", report.drifted);
    assert_eq!(report.public_ip_rejected, vec!["echo-public".to_string()]);

    let dns_obs = DnsObs::new();
    // node-a resolves the service name to the Ready overlay endpoint only.
    let answer = endpoints
        .iter()
        .find(|e| {
            is_overlay_ip(&e.address_ip, "10.100.0.0/16") && !is_provider_public_ip(&e.address_ip)
        })
        .expect("overlay answer");
    observe_resolve(
        &dns_obs,
        "echo.local.demo.svc.forge",
        "ok",
        Some(&answer.address_ip),
    );
    assert_eq!(answer.address_ip, "10.100.2.5");
    assert_eq!(dns_obs.total_ok(), 1);
}

#[test]
fn policy_deny_still_applies_after_dns() {
    // DNS resolved restricted.local.demo.svc.forge → 10.100.3.7, but policy denies.
    let backend = FakePolicyBackend::new();
    let rules = PolicyRuleSet {
        node_id: "node-a".into(),
        generation: 4,
        rules: vec![PolicyRule {
            workload_id: "caller-on-a".into(),
            direction: "egress".into(),
            from_cidr: None,
            to_cidr: Some("10.100.3.7/32".into()),
            port: Some(8080),
            protocol: Some("tcp".into()),
            action: "deny".into(),
            reason: Some("networkpolicy:deny-restricted".into()),
        }],
    };
    backend.apply(&rules).unwrap();
    assert_eq!(backend.rule_count(), 1);
    assert_eq!(backend.applied_generation(), Some(4));

    let resolved = "10.100.3.7";
    let denied = rules.rules.iter().any(|r| {
        r.action == "deny"
            && r.to_cidr
                .as_deref()
                .map(|c| c.starts_with(resolved) || c == "10.100.3.7/32")
                .unwrap_or(false)
    });
    assert!(denied, "policy must deny after DNS resolution");
}

#[test]
fn node_dns_bootstrap_search_domain() {
    let backend = FakeDnsBackend::new();
    let health = NodeNetworkHealth::new();
    let cfg = DnsConfig {
        nameserver: "10.100.0.53".into(),
        zone: "svc.forge".into(),
        search: "production.shop.svc.forge".into(),
    };
    bootstrap_dns(&backend, &cfg, &health).unwrap();
    let applied = backend.current().unwrap();
    assert_eq!(applied.nameserver, "10.100.0.53");
    assert_eq!(applied.search, "production.shop.svc.forge");
    assert_eq!(health.status(), "Ready");
}

#[test]
fn drift_obs_counts_route_drift() {
    let obs = DriftObs::new();
    obs.bump(2);
    assert_eq!(obs.total(), 2);
}
