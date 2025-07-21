package czds

import (
	"compress/gzip"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
	"github.com/miekg/dns"
	"github.com/moos3/bell/config"
	_ "golang.org/x/net/publicsuffix"
)

var validRecordTypes = map[string]bool{
	"NS":     true,
	"A":      true,
	"AAAA":   true,
	"MX":     true,
	"TXT":    true,
	"CNAME":  true,
	"SOA":    true,
	"PTR":    true,
	"SRV":    true,
	"CAA":    true,
	"DNSKEY": true,
	"DS":     true,
}

func parseZoneFile(reader io.Reader, tld string, batchSize int, processBatch func(records []map[string]interface{}, nameservers map[string][]string) error) error {
	zp := dns.NewZoneParser(reader, tld+".", "")
	records := make([]map[string]interface{}, 0, batchSize)
	nameservers := make(map[string][]string)

	for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
		if rr == nil {
			continue
		}
		domain := rr.Header().Name
		// Skip root TLD (e.g., aero.)
		if domain == tld+"." {
			log.Printf("Skipping root TLD domain %s in TLD %s", domain, tld)
			continue
		}
		// Remove trailing dot from domain
		domain = strings.TrimSuffix(domain, ".")
		if domain == "" {
			log.Printf("Skipping empty domain after trimming in TLD %s", tld)
			continue
		}
		recordType := dns.TypeToString[rr.Header().Rrtype]
		if !validRecordTypes[recordType] {
			log.Printf("Skipping unsupported record type %s for domain %s in TLD %s", recordType, domain, tld)
			continue
		}
		records = append(records, map[string]interface{}{
			"domain_name": domain,
			"record_type": recordType,
			"record_data": rr.String(),
			"ttl":         int(rr.Header().Ttl),
			"tld":         tld,
			"source":      "CZDS",
		})
		if recordType == "NS" {
			if ns, ok := rr.(*dns.NS); ok {
				nsName := strings.TrimSuffix(ns.Ns, ".")
				if nsName == "" {
					log.Printf("Skipping empty nameserver for domain %s in TLD %s", domain, tld)
					continue
				}
				nameservers[domain] = append(nameservers[domain], nsName)
			}
		}
		// Process batch when full
		if len(records) >= batchSize {
			if err := processBatch(records, nameservers); err != nil {
				return err
			}
			// Clear memory
			records = make([]map[string]interface{}, 0, batchSize)
			nameservers = make(map[string][]string)
		}
	}
	if err := zp.Err(); err != nil {
		return fmt.Errorf("error parsing zone file: %v", err)
	}
	// Process remaining records
	if len(records) > 0 {
		if err := processBatch(records, nameservers); err != nil {
			return err
		}
	}
	return nil
}

func storeRecords(db *sql.DB, records []map[string]interface{}, nameservers map[string][]string, tld string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	domainStmt, err := tx.Prepare(`
		INSERT INTO domains (domain_name, tld, nameservers, last_updated)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (domain_name, tld) DO UPDATE
		SET nameservers = EXCLUDED.nameservers, last_updated = EXCLUDED.last_updated
		RETURNING id
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer domainStmt.Close()

	recordStmt, err := tx.Prepare(`
		INSERT INTO dns_records (domain_id, record_type, record_data, ttl, source, last_updated)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer recordStmt.Close()

	domainIDs := make(map[string]int)
	for _, r := range records {
		domain := r["domain_name"].(string)
		if _, exists := domainIDs[domain]; !exists {
			var domainID int
			ns := nameservers[domain]
			if len(ns) == 0 {
				ns = []string{}
			}
			err := domainStmt.QueryRow(domain, tld, pq.StringArray(ns), time.Now().UTC()).Scan(&domainID)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to insert domain %s: %v", domain, err)
			}
			domainIDs[domain] = domainID
		}
	}

	for _, r := range records {
		domain := r["domain_name"].(string)
		domainID := domainIDs[domain]
		_, err := recordStmt.Exec(
			domainID,
			r["record_type"],
			r["record_data"],
			r["ttl"],
			r["source"],
			time.Now().UTC(),
		)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to insert record for %s: %v", domain, err)
		}
	}

	return tx.Commit()
}

func getProcessedTLDs(db *sql.DB) (map[string]time.Time, error) {
	rows, err := db.Query("SELECT tld, last_processed FROM processed_tlds")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	processed := make(map[string]time.Time)
	for rows.Next() {
		var tld string
		var lastProcessed time.Time
		if err := rows.Scan(&tld, &lastProcessed); err != nil {
			return nil, err
		}
		processed[tld] = lastProcessed
	}
	return processed, rows.Err()
}

func markTLDProcessed(db *sql.DB, tld string) error {
	_, err := db.Exec(`
		INSERT INTO processed_tlds (tld, last_processed)
		VALUES ($1, $2)
		ON CONFLICT (tld) DO UPDATE SET last_processed = $2
	`, tld, time.Now().UTC())
	return err
}

func processZoneFile(db *sql.DB, entry os.DirEntry, force bool, processedTLDs map[string]time.Time, reprocessThreshold time.Duration, batchSize int, zonesDir string) error {
	if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt.gz") {
		return nil
	}

	tld := strings.TrimSuffix(entry.Name(), ".txt.gz")
	if tld == "" {
		return fmt.Errorf("invalid file: %s (no TLD)", entry.Name())
	}

	if !force {
		if lastProcessed, exists := processedTLDs[tld]; exists && time.Since(lastProcessed) < reprocessThreshold {
			fmt.Printf("Skipping TLD %s: processed recently at %v\n", tld, lastProcessed)
			return nil
		}
	}

	fmt.Printf("Processing TLD: %s\n", tld)
	filePath := filepath.Join(zonesDir, entry.Name())
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("error opening zone file for %s: %v", tld, err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("error decompressing zone file for %s: %v", tld, err)
	}
	defer gzReader.Close()

	err = parseZoneFile(gzReader, tld, batchSize, func(records []map[string]interface{}, nameservers map[string][]string) error {
		if err := storeRecords(db, records, nameservers, tld); err != nil {
			return fmt.Errorf("error storing records for %s: %v", tld, err)
		}
		fmt.Printf("Stored %d records for %s\n", len(records), tld)
		return nil
	})
	if err != nil {
		return err
	}

	if err := markTLDProcessed(db, tld); err != nil {
		return fmt.Errorf("error marking %s as processed: %v", tld, err)
	}
	fmt.Printf("Completed processing %s\n", tld)
	return nil
}

func main() {
	force := flag.Bool("force", false, "Force reprocessing of all TLDs")
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

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
		log.Fatal("Failed to connect to AlloyDB via private IP: ", err)
	}
	fmt.Println("Connected to AlloyDB successfully.")

	if _, err := os.Stat(config.Zones.Directory); os.IsNotExist(err) {
		log.Fatal("Zones directory does not exist: ", config.Zones.Directory)
	}

	entries, err := os.ReadDir(config.Zones.Directory)
	if err != nil {
		log.Fatal("Failed to read zones directory: ", err)
	}

	processedTLDs, err := getProcessedTLDs(db)
	if err != nil {
		log.Fatal(err)
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, config.Zones.MaxConcurrent)
	reprocessThreshold := time.Duration(config.Zones.ReprocessThresholdHours) * time.Hour

	for _, entry := range entries {
		wg.Add(1)
		go func(entry os.DirEntry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := processZoneFile(db, entry, *force, processedTLDs, reprocessThreshold, config.Zones.BatchSize, config.Zones.Directory); err != nil {
				log.Printf("Error processing %s: %v", entry.Name(), err)
			}
		}(entry)
	}
	wg.Wait()
}
