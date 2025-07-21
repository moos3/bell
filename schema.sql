-- GRPC / REST API Tables
-- API keys table for authentication
CREATE TABLE api_keys (
                          api_key UUID PRIMARY KEY,
                          description VARCHAR(255),
                          created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                          is_active BOOLEAN DEFAULT TRUE
);

-- Index for faster lookup
CREATE INDEX idx_api_keys_api_key ON api_keys (api_key);

-- Example API key (generate UUID with `uuid_generate_v4()` or tool)
-- INSERT INTO api_keys (api_key, description) VALUES ('550e8400-e29b-41d4-a716-446655440000', 'Test API Key');

CREATE DATABASE dns_records_db;

  -- Domains table: Stores unique domains and their nameservers
CREATE TABLE domains (
                         id SERIAL PRIMARY KEY,
                         domain_name VARCHAR(255) NOT NULL,
                         tld VARCHAR(50) NOT NULL,
                         nameservers TEXT[] NOT NULL DEFAULT '{}', -- Array of nameserver hostnames
                         last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                         UNIQUE (domain_name, tld)
);

-- DNS records table: Stores all DNS records (NS, A, AAAA, MX, TXT, etc.)
CREATE TABLE dns_records (
                             id BIGSERIAL, -- No PRIMARY KEY on parent table for partitioning
                             domain_id INTEGER NOT NULL REFERENCES domains(id),
                             record_type VARCHAR(20) NOT NULL,
                             record_data TEXT NOT NULL,
                             ttl INTEGER,
                             source VARCHAR(20) DEFAULT 'CZDS',
                             last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP
) PARTITION BY LIST (record_type);

-- Partitions
CREATE TABLE dns_records_ns PARTITION OF dns_records FOR VALUES IN ('NS');
CREATE TABLE dns_records_a PARTITION OF dns_records FOR VALUES IN ('A');
CREATE TABLE dns_records_aaaa PARTITION OF dns_records FOR VALUES IN ('AAAA');
CREATE TABLE dns_records_mx PARTITION OF dns_records FOR VALUES IN ('MX');
CREATE TABLE dns_records_txt PARTITION OF dns_records FOR VALUES IN ('TXT');
CREATE TABLE dns_records_cname PARTITION OF dns_records FOR VALUES IN ('CNAME');
CREATE TABLE dns_records_other PARTITION OF dns_records FOR VALUES IN ('SOA', 'PTR', 'SRV', 'CAA', 'DNSKEY', 'DS');

-- Add PRIMARY KEY constraints to partitions
ALTER TABLE dns_records_ns ADD CONSTRAINT dns_records_ns_pk PRIMARY KEY (id);
ALTER TABLE dns_records_a ADD CONSTRAINT dns_records_a_pk PRIMARY KEY (id);
ALTER TABLE dns_records_aaaa ADD CONSTRAINT dns_records_aaaa_pk PRIMARY KEY (id);
ALTER TABLE dns_records_mx ADD CONSTRAINT dns_records_mx_pk PRIMARY KEY (id);
ALTER TABLE dns_records_txt ADD CONSTRAINT dns_records_txt_pk PRIMARY KEY (id);
ALTER TABLE dns_records_cname ADD CONSTRAINT dns_records_cname_pk PRIMARY KEY (id);
ALTER TABLE dns_records_other ADD CONSTRAINT dns_records_other_pk PRIMARY KEY (id);
-- Indexes
CREATE INDEX idx_domains_domain_name ON domains (domain_name);
CREATE INDEX idx_domains_tld ON domains (tld);
CREATE INDEX idx_dns_records_domain_id ON dns_records (domain_id);
CREATE INDEX idx_dns_records_record_type ON dns_records (record_type);

-- Processed TLDs table (unchanged)
CREATE TABLE processed_tlds (
                                tld VARCHAR(50) PRIMARY KEY,
                                last_processed TIMESTAMP NOT NULL
);

-- Query progress table to track last processed domain_id
CREATE TABLE query_progress (
                                id SERIAL PRIMARY KEY,
                                last_domain_id INTEGER REFERENCES domains(id),
                                updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Index for faster lookup
CREATE INDEX idx_query_progress_id ON query_progress (id);

-- Initialize with no progress
INSERT INTO query_progress (last_domain_id) VALUES (NULL);

