CREATE TABLE IF NOT EXISTS "namespace-configs" (
    namespace text NOT NULL,
    config jsonb,
    "timestamp" timestamp with time zone NOT NULL,
    PRIMARY KEY (namespace, "timestamp")
);

CREATE TABLE IF NOT EXISTS "namespace-changelog" (
    namespace text NOT NULL,
    operation text NOT NULL,
    config jsonb,
    "timestamp" timestamp with time zone NOT NULL,
    PRIMARY KEY (namespace, operation, "timestamp")
);

CREATE TABLE IF NOT EXISTS "changelog" (
    namespace text NOT NULL,
    operation text NOT NULL,
    relationtuple text NOT NULL,
    "timestamp" timestamp with time zone NOT NULL,
    PRIMARY KEY (namespace, operation, relationtuple, "timestamp")
);

CREATE TABLE IF NOT EXISTS "namespace-relation-lookup" (
    namespace text NOT NULL,
    relation text NOT NULL,
    relationtuple text NOT NULL,
    PRIMARY KEY (namespace, relation, relationtuple)
);