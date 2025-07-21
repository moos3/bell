package bell

import (
	"context"
	"log"
	"time"

	"github.com/moos3/bell/client"
)

func main() {
	// Test client
	client, err := client.NewClient("34.21.17.237:50051")
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	apiKey := "550e8400-e29b-41d4-a716-446655440000"
	valid, message, err := client.Authenticate(ctx, apiKey)
	if err != nil {
		log.Fatalf("Authentication error: %v", err)
	}
	if !valid {
		log.Fatalf("Authentication failed: %s", message)
	}
	log.Printf("Authentication successful: %s", message)

	records, err := client.GetRecords(ctx, apiKey, "917182.baby", []string{"A"})
	if err != nil {
		log.Fatalf("Failed to get records: %v", err)
	}
	for _, r := range records {
		log.Printf("Record: type=%s, data=%s, ttl=%d, source=%s, last_updated=%s",
			r.RecordType, r.RecordData, r.Ttl, r.Source, r.LastUpdated)
	}
}
