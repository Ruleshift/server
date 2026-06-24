CREATE TABLE module_versions (
    developer_id TEXT NOT NULL,
    module_id TEXT NOT NULL,
    version TEXT NOT NULL,
    image_ref TEXT NOT NULL,
    image_digest TEXT NOT NULL,
    abi_version INTEGER NOT NULL,
    descriptor_digest TEXT NOT NULL,
    descriptor_set BYTEA NOT NULL,
    manifest JSONB NOT NULL,
    credential_name TEXT NOT NULL DEFAULT '',
    lifecycle_status TEXT NOT NULL,
    endpoint TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (developer_id, module_id, version),
    FOREIGN KEY (developer_id, module_id) REFERENCES modules(developer_id, module_key) ON DELETE RESTRICT,
    UNIQUE (developer_id, module_id, version, image_digest)
);

CREATE INDEX module_versions_status_idx ON module_versions(developer_id, module_id, lifecycle_status);

CREATE TABLE module_validation_runs (
    developer_id TEXT NOT NULL,
    module_id TEXT NOT NULL,
    version TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    result TEXT NOT NULL,
    logs TEXT NOT NULL,
    PRIMARY KEY (developer_id, module_id, version),
    FOREIGN KEY (developer_id, module_id, version)
        REFERENCES module_versions(developer_id, module_id, version) ON DELETE CASCADE
);

CREATE TABLE room_routes (
    room_id TEXT PRIMARY KEY,
    developer_id TEXT NOT NULL,
    module_id TEXT NOT NULL,
    module_version TEXT NOT NULL,
    image_digest TEXT NOT NULL,
    module_database TEXT NOT NULL,
    seed NUMERIC(20, 0) NOT NULL CHECK (seed >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (developer_id, module_id, module_version)
        REFERENCES module_versions(developer_id, module_id, version) ON DELETE RESTRICT
);

CREATE INDEX room_routes_tenant_idx ON room_routes(developer_id, module_id);
