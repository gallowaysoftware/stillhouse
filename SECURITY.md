# Security policy

## Reporting a vulnerability

Please report security issues privately via GitHub's **Report a
vulnerability** button on the Security tab, rather than opening a public
issue.

Include what you did, what happened, and what you expected. A proof of
concept helps. You'll get an acknowledgement; this is a small project, so
please be patient on timelines.

## Scope worth knowing about

Stillhouse holds excise records for a licensed spirits producer. The
things most worth reporting:

- **Tenant isolation.** One tenant is one CRA spirits licence. The server
  connects as a non-superuser role so PostgreSQL row-level security
  enforces the boundary, and there's an integration test for it. Any way
  to read or write across tenants is the highest-severity issue here.
- **Authentication.** Session cookies (browser) and `Authorization:
  Bearer sh_…` API tokens (MCP and scripts) both resolve to a user. Token
  values are stored only as SHA-256 hashes.
- **Role gating.** Endpoints are gated by role and fail closed — an
  endpoint absent from the gate map requires owner. A write reachable
  below its intended role is a real finding.
- **Audit integrity.** Production gauges, bottlings, removals and B266
  submissions are audited. Anything that lets a recorded event be altered
  or removed without a trace matters.

## Not in scope

Self-hosted deployments where the operator has exposed Postgres directly,
chosen weak `.env` passwords, or run the app as the superuser role — the
deploy docs warn against all three.
