CREATE TABLE public.legacy_registry (
    legacy_id bigint PRIMARY KEY,
    legacy_type text NOT NULL CHECK (legacy_type IN ('site', 'asset', 'device')),
    external_key text NOT NULL UNIQUE,
    display_name text NOT NULL,
    source_revision bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE public.not_captured (
    id bigint PRIMARY KEY,
    note text NOT NULL
);

INSERT INTO public.legacy_registry (legacy_id, legacy_type, external_key, display_name)
VALUES (1001, 'site', 'legacy-site-a', 'Legacy Site A');

INSERT INTO public.not_captured (id, note)
VALUES (1, 'This table must never appear in the CDC evidence.');
