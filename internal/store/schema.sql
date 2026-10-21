-- Timestamps are stored as RFC 3339 text in UTC (2026-01-02T15:04:05Z), which
-- sorts chronologically as plain text.

CREATE TABLE services (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    team TEXT NOT NULL
);

-- A deployment to production. A remediation deployment is unplanned work
-- shipped to fix a user-facing problem; it counts towards the rework rate.
CREATE TABLE deployments (
    id          INTEGER PRIMARY KEY,
    service_id  INTEGER NOT NULL REFERENCES services (id),
    deployed_at TEXT    NOT NULL,
    remediation INTEGER NOT NULL DEFAULT 0 CHECK (remediation IN (0, 1))
);

CREATE INDEX deployments_service_deployed_at ON deployments (service_id, deployed_at);

-- A commit that reached production in a deployment.
CREATE TABLE changes (
    id            INTEGER PRIMARY KEY,
    deployment_id INTEGER NOT NULL REFERENCES deployments (id),
    sha           TEXT    NOT NULL,
    committed_at  TEXT    NOT NULL
);

CREATE INDEX changes_deployment_id ON changes (deployment_id);

-- A production incident. caused_by_deployment_id is null for incidents that no
-- deployment caused (an infrastructure outage, say); resolved_at is null while
-- the incident is open.
CREATE TABLE incidents (
    id                      INTEGER PRIMARY KEY,
    service_id              INTEGER NOT NULL REFERENCES services (id),
    caused_by_deployment_id INTEGER REFERENCES deployments (id),
    severity                TEXT    NOT NULL CHECK (severity IN ('sev1', 'sev2', 'sev3')),
    title                   TEXT    NOT NULL,
    started_at              TEXT    NOT NULL,
    resolved_at             TEXT
);

CREATE INDEX incidents_service_started_at ON incidents (service_id, started_at);
CREATE INDEX incidents_caused_by_deployment_id ON incidents (caused_by_deployment_id);
