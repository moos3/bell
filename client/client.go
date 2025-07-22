// Package client provides a client for interacting with the DNS service
// over gRPC, supporting authentication and DNS record retrieval.
package client

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	pb "github.com/moos3/bell/pb/bell/v1"
)

// Client encapsulates a gRPC client for the DNS service.
type Client struct {
	conn   *grpc.ClientConn    // gRPC connection to the server
	client pb.DNSServiceClient // DNS service client interface
}

// NewClient initializes a new DNS service client connected to the specified server address.
//
// It returns a Client instance or an error if the connection fails.
func NewClient(serverAddr string) (*Client, error) {
	conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server at %s: %v", serverAddr, err)
	}
	client := pb.NewDNSServiceClient(conn)
	return &Client{conn: conn, client: client}, nil
}

// Close closes the gRPC client connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Authenticate validates an API key with the DNS service.
//
// It sends the API key in the gRPC metadata and returns whether the key is valid,
// along with a message from the server and any error encountered.
func (c *Client) Authenticate(ctx context.Context, apiKey string) (bool, string, error) {
	// Add API key to metadata
	ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", apiKey)
	resp, err := c.client.Authenticate(ctx, &pb.AuthenticateRequest{ApiKey: apiKey})
	if err != nil {
		return false, "", fmt.Errorf("authentication failed: %v", err)
	}
	return resp.Valid, resp.Message, nil
}

// GetRecords fetches DNS records for a specified domain from the DNS service.
//
// It requires a valid API key in the gRPC metadata and optional record types
// (e.g., A, AAAA) to filter results. It returns a slice of DNSRecord structs
// or an error if the request fails.
func (c *Client) GetRecords(ctx context.Context, apiKey, domain string, recordTypes []string) ([]*pb.DNSRecord, error) {
	// Add API key to metadata
	ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", apiKey)
	resp, err := c.client.GetRecords(ctx, &pb.GetRecordsRequest{
		Domain:     domain,
		RecordType: recordTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch records for %s: %v", domain, err)
	}
	return resp.Records, nil
}

// Example demonstrates usage of the Client to authenticate and fetch DNS records.
func Example() {
	// Initialize client
	client, err := NewClient("localhost:50051")
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Create context
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Authenticate
	apiKey := "550e8400-e29b-41d4-a716-446655440000"
	valid, message, err := client.Authenticate(ctx, apiKey)
	if err != nil {
		log.Fatalf("Authentication error: %v", err)
	}
	if !valid {
		log.Fatalf("Authentication failed: %s", message)
	}
	log.Printf("Authentication successful: %s", message)

	// Fetch records
	records, err := client.GetRecords(ctx, apiKey, "917182.baby", []string{"A"})
	if err != nil {
		log.Fatalf("Failed to get records: %v", err)
	}
	for _, r := range records {
		log.Printf("Record: type=%s, data=%s, ttl=%d, source=%s, last_updated=%s",
			r.RecordType, r.RecordData, r.Ttl, r.Source, r.LastUpdated)
	}
}
