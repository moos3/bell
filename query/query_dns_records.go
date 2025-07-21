package query

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/lib/pq"
	"github.com/miekg/dns"
	"github.com/moos3/bell/config"
)

var recordTypes = []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeMX, dns.TypeTXT, dns.TypeCNAME}

type DomainInfo struct {
	ID          int
	Domain      string
	TLD         string
	Nameservers pq.StringArray
}

func getDomainsAndNameservers(db *sql.DB, lastDomainID *int, batchSize int) ([]DomainInfo, error) {
	query := `
		SELECT id, domain_name, tld, nameservers
		FROM domains
		WHERE nameservers != '{}'
		AND (last_updated IS NULL OR last_updated < NOW() - INTERVAL '12 hours')
	`
	args := []interface{}{}
	if lastDomainID != nil {
		query += " AND id > $1"
		args = append(args, *lastDomainID)
	}
	query += fmt.Sprintf(" ORDER BY id LIMIT %d", batchSize)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var domains []DomainInfo
	for rows.Next() {
		var d DomainInfo
		if err := rows.Scan(&d.ID, &d.Domain, &d.TLD, &d.Nameservers); err != nil {
			return nil, fmt.Errorf("failed to scan domain: %v", err)
		}
		domains = append(domains, d)
	}
	return domains, rows.Err()
}

func updateProgress(db *sql.DB, domainID int) error {
	_, err := db.Exec(`
		UPDATE query_progress
		SET last_domain_id = $1, updated_at = $2
		WHERE id = 1
	`, domainID, time.Now().UTC())
	return err
}

func queryDNSRecords(domain string, domainID int, nameservers []string, recordType uint16, dnsServers []string) ([]map[string]interface{}, error) {
	client := &dns.Client{Timeout: 10 * time.Second}
	var records []map[string]interface{}

	// Remove trailing dot from domain
	domain = strings.TrimSuffix(domain, ".")

	if len(nameservers) == 0 {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(domain), dns.TypeNS)
		dnsServer := dnsServers[rand.Intn(len(dnsServers))]
		var r *dns.Msg
		err := backoff.Retry(func() error {
			var err error
			r, _, err = client.Exchange(m, dnsServer)
			return err
		}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 3))
		if err != nil {
			return nil, fmt.Errorf("failed to query NS for %s using %s after retries: %v", domain, dnsServer, err)
		}
		for _, ans := range r.Answer {
			if ns, ok := ans.(*dns.NS); ok {
				nsName := strings.TrimSuffix(ns.Ns, ".")
				if nsName == "" {
					continue
				}
				nameservers = append(nameservers, nsName)
			}
		}
	}

	for _, ns := range nameservers {
		nsAddr := ns + ":53"
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(domain), recordType)
		var r *dns.Msg
		err := backoff.Retry(func() error {
			var err error
			r, _, err = client.Exchange(m, nsAddr)
			return err
		}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 3))
		if err != nil {
			log.Printf("Error querying %s for %s using %s after retries: %v", dns.TypeToString[recordType], domain, nsAddr, err)
			continue
		}
		for _, ans := range r.Answer {
			records = append(records, map[string]interface{}{
				"domain_id":   domainID,
				"record_type": dns.TypeToString[recordType],
				"record_data": ans.String(),
				"ttl":         int(ans.Header().Ttl),
				"source":      "QUERY",
			})
		}
		if len(records) > 0 {
			break
		}
	}
	return records, nil
}

func processDomain(db *sql.DB, domainInfo DomainInfo, dnsServers []string) error {
	fmt.Printf("Processing domain: %s\n", domainInfo.Domain)
	for i, rt := range recordTypes {
		records, err := queryDNSRecords(domainInfo.Domain, domainInfo.ID, domainInfo.Nameservers, rt, dnsServers)
		if err != nil {
			log.Printf("Error querying %s for %s: %v", dns.TypeToString[rt], domainInfo.Domain, err)
			continue
		}
		if len(records) > 0 {
			if err := storeRecords(db, records); err != nil {
				log.Printf("Error storing records for %s: %v", domainInfo.Domain, err)
			} else {
				fmt.Printf("Stored %d %s records for %s\n", len(records), dns.TypeToString[rt], domainInfo.Domain)
			}
		}
		// Add 5-second delay between record types, except for the last one
		if i < len(recordTypes)-1 {
			time.Sleep(5 * time.Second)
		}
	}
	// Update progress
	if err := updateProgress(db, domainInfo.ID); err != nil {
		log.Printf("Error updating progress for domain %s: %v", domainInfo.Domain, err)
	}
	return nil
}

func storeRecords(db *sql.DB, records []map[string]interface{}) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO dns_records (domain_id, record_type, record_data, ttl, source, last_updated)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range records {
		_, err := stmt.Exec(
			r["domain_id"],
			r["record_type"],
			r["record_data"],
			r["ttl"],
			r["source"],
			time.Now().UTC(),
		)
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	// Update domains.last_updated
	_, err = tx.Exec(`
		UPDATE domains
		SET last_updated = $1
		WHERE id = $2
	`, time.Now().UTC(), records[0]["domain_id"])
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func main() {
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Seed random number generator
	rand.Seed(time.Now().UnixNano())

	// Load configuration
	config, err := config.LoadConfig(*configFile)
	if err != nil {
		log.Fatal(err)
	}

	// Connect to AlloyDB
	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		config.AlloyDB.Host, config.AlloyDB.Port, config.AlloyDB.User, config.AlloyDB.Password, config.AlloyDB.Database, config.AlloyDB.SSLMode,
	)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal("Failed to connect to AlloyDB: ", err)
	}
	fmt.Println("Connected to AlloyDB successfully.")

	// Get last processed domain_id
	var lastDomainID sql.NullInt32
	err = db.QueryRow("SELECT last_domain_id FROM query_progress WHERE id = 1").Scan(&lastDomainID)
	if err != nil {
		log.Fatal("Failed to get last domain ID: ", err)
	}
	var lastDomainIDPtr *int
	if lastDomainID.Valid {
		lastDomainIDVal := int(lastDomainID.Int32)
		lastDomainIDPtr = &lastDomainIDVal
	}

	// Process domains in batches
	batchSize := config.DNSQuery.BatchSize
	for {
		domains, err := getDomainsAndNameservers(db, lastDomainIDPtr, batchSize)
		if err != nil {
			log.Fatal("Failed to fetch domains: ", err)
		}
		if len(domains) == 0 {
			fmt.Println("No more domains to process.")
			break
		}

		var wg sync.WaitGroup
		sem := make(chan struct{}, config.DNSQuery.MaxConcurrent)

		for _, d := range domains {
			wg.Add(1)
			go func(domainInfo DomainInfo) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						log.Printf("Recovered from panic while processing %s: %v", domainInfo.Domain, r)
					}
				}()
				sem <- struct{}{}
				defer func() { <-sem }()
				if err := processDomain(db, domainInfo, config.DNSQuery.DNSServers); err != nil {
					log.Printf("Error processing domain %s: %v", domainInfo.Domain, err)
				}
				// Update lastDomainIDPtr for the next batch
				lastDomainIDPtr = &domainInfo.ID
			}(d)
		}
		wg.Wait()
	}
}
