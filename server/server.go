// Package server provides a gRPC server implementation for the DNS service,
// handling authentication and DNS record retrieval from an AlloyDB database.
// It exposes REST endpoints via gRPC-Gateway with CORS support.
package server

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	_ "github.com/google/uuid"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	_ "github.com/lib/pq"
	"github.com/rs/cors"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/moos3/bell/config"
	pb "github.com/moos3/bell/pb/bell/v1"
)

// server implements the DNSService gRPC interface, handling authentication
// and DNS record queries against an AlloyDB database.
type server struct {
	pb.UnimplementedDNSServiceServer
	db *sql.DB // Database connection
}

// Authenticate validates an API key against the api_keys table in AlloyDB.
//
// It returns an AuthenticateResponse indicating whether the key is valid
// and an optional message describing the result.
func (s *server) Authenticate(ctx context.Context, req *pb.AuthenticateRequest) (*pb.AuthenticateResponse, error) {
	var isActive bool
	err := s.db.QueryRow("SELECT is_active FROM api_keys WHERE api_key = $1", req.ApiKey).Scan(&isActive)
	if err == sql.ErrNoRows {
		log.Printf("Authenticate: API key %s not found", req.ApiKey)
		return &pb.AuthenticateResponse{Valid: false, Message: "Invalid API key"}, nil
	}
	if err != nil {
		log.Printf("Authenticate: Failed to validate API key %s: %v", req.ApiKey, err)
		return nil, status.Errorf(codes.Internal, "failed to validate API key: %v", err)
	}
	if !isActive {
		log.Printf("Authenticate: API key %s is inactive", req.ApiKey)
		return &pb.AuthenticateResponse{Valid: false, Message: "API key is inactive"}, nil
	}
	log.Printf("Authenticate: API key %s is valid", req.ApiKey)
	return &pb.AuthenticateResponse{Valid: true, Message: "API key is valid"}, nil
}

// GetRecords retrieves DNS records for a specified domain from AlloyDB.
//
// It requires a valid API key in the gRPC metadata ("x-api-key") and
// returns a GetRecordsResponse containing the matching DNS records.
// Optional record types (e.g., A, AAAA) can be specified to filter results.
func (s *server) GetRecords(ctx context.Context, req *pb.GetRecordsRequest) (*pb.GetRecordsResponse, error) {
	// Log metadata for debugging
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		log.Println("GetRecords: Missing metadata")
		return nil, status.Errorf(codes.Unauthenticated, "missing metadata")
	}
	log.Printf("GetRecords: Metadata received: %v", md)

	// Validate API key from metadata
	apiKeys := md.Get("x-api-key")
	if len(apiKeys) == 0 {
		log.Println("GetRecords: Missing API key in metadata")
		return nil, status.Errorf(codes.Unauthenticated, "missing API key")
	}
	var isActive bool
	apiKey := apiKeys[0]
	err := s.db.QueryRow("SELECT is_active FROM api_keys WHERE api_key = $1", apiKey).Scan(&isActive)
	if err == sql.ErrNoRows {
		log.Printf("GetRecords: API key %s not found", apiKey)
		return nil, status.Errorf(codes.Unauthenticated, "invalid API key")
	}
	if err != nil {
		log.Printf("GetRecords: Failed to validate API key %s: %v", apiKey, err)
		return nil, status.Errorf(codes.Internal, "failed to validate API key: %v", err)
	}
	if !isActive {
		log.Printf("GetRecords: API key %s is inactive", apiKey)
		return nil, status.Errorf(codes.Unauthenticated, "API key is inactive")
	}

	// Query records
	query := `
		SELECT r.domain_id, r.record_type, r.record_data, r.ttl, r.source, r.last_updated
		FROM domains d
		JOIN dns_records r ON d.id = r.domain_id
		WHERE d.domain_name = $1
	`
	args := []interface{}{req.Domain}
	if len(req.RecordType) > 0 {
		query += fmt.Sprintf(" AND r.record_type IN (%s)", generatePlaceholders(2, len(req.RecordType)))
		for _, rt := range req.RecordType {
			args = append(args, rt)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		log.Printf("GetRecords: Failed to query records for domain %s: %v", req.Domain, err)
		return nil, status.Errorf(codes.Internal, "failed to query records: %v", err)
	}
	defer rows.Close()

	var records []*pb.DNSRecord
	for rows.Next() {
		var r pb.DNSRecord
		var lastUpdated time.Time
		if err := rows.Scan(&r.DomainId, &r.RecordType, &r.RecordData, &r.Ttl, &r.Source, &lastUpdated); err != nil {
			log.Printf("GetRecords: Failed to scan record for domain %s: %v", req.Domain, err)
			return nil, status.Errorf(codes.Internal, "failed to scan record: %v", err)
		}
		r.LastUpdated = lastUpdated.Format(time.RFC3339)
		records = append(records, &r)
	}
	if err := rows.Err(); err != nil {
		log.Printf("GetRecords: Failed to iterate records for domain %s: %v", req.Domain, err)
		return nil, status.Errorf(codes.Internal, "failed to iterate records: %v", err)
	}
	log.Printf("GetRecords: Response for domain %s: %v records", req.Domain, len(records))
	for _, r := range records {
		log.Printf("GetRecords: Record for %s: type=%s, data=%s, ttl=%d, source=%s, last_updated=%s",
			req.Domain, r.RecordType, r.RecordData, r.Ttl, r.Source, r.LastUpdated)
	}
	return &pb.GetRecordsResponse{Records: records}, nil
}

// generatePlaceholders creates a comma-separated string of PostgreSQL placeholders
// (e.g., "$2,$3") starting from the given index and count.
func generatePlaceholders(start, count int) string {
	placeholders := make([]string, count)
	for i := 0; i < count; i++ {
		placeholders[i] = fmt.Sprintf("$%d", start+i)
	}
	return strings.Join(placeholders, ",")
}

// logHeadersMiddleware logs all HTTP request headers before passing the request
// to the next handler in the chain.
func logHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Request: %s %s", r.Method, r.URL.String())
		log.Println("Headers:")
		for name, values := range r.Header {
			for _, value := range values {
				log.Printf("  %s: %s", name, value)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// main starts the gRPC server and gRPC-Gateway with CORS support.
func main() {
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	grpcPort := flag.String("grpc-port", ":50051", "gRPC server port")
	httpPort := flag.String("http-port", ":8080", "HTTP server port")
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
		log.Fatal("Failed to connect to AlloyDB: ", err)
	}
	fmt.Println("Connected to AlloyDB successfully.")

	// Start gRPC server
	grpcServer := grpc.NewServer()
	s := &server{db: db}
	pb.RegisterDNSServiceServer(grpcServer, s)
	lis, err := net.Listen("tcp", *grpcPort)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", *grpcPort, err)
	}

	// Start gRPC-Gateway with CORS and case-insensitive header matcher
	ctx := context.Background()
	gwmux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(func(header string) (string, bool) {
			if strings.EqualFold(header, "X-API-Key") {
				log.Printf("Mapping header %s to x-api-key", header)
				return "x-api-key", true
			}
			return header, false
		}),
	)
	opts := []grpc.DialOption{grpc.WithInsecure()}
	err = pb.RegisterDNSServiceHandlerFromEndpoint(ctx, gwmux, *grpcPort, opts)
	if err != nil {
		log.Fatalf("Failed to register gateway: %v", err)
	}

	// Configure CORS
	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"X-API-Key", "x-api-key", "Content-Type"},
		AllowCredentials: true,
	})

	// Chain middlewares: log headers, then CORS, then gRPC-Gateway
	mux := http.NewServeMux()
	mux.Handle("/", logHeadersMiddleware(corsMiddleware.Handler(gwmux)))
	server := &http.Server{
		Addr:    *httpPort,
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}
	go func() {
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("Failed to serve HTTP: %v", err)
		}
	}()

	fmt.Printf("gRPC server listening on %s\nHTTP server listening on %s\n", *grpcPort, *httpPort)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve gRPC: %v", err)
	}
}
