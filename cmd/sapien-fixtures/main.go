// Command sapien-fixtures runs the three logistics mock services
// (order-service, allocation-service, rider-service) used by Sapien's
// fixtures, phase tests, and the demo. See
// fixtures/logistics/README.md for the seed data and success scenario.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gs-sinha/sapien/fixtures/logistics/mock"
)

func main() {
	orderPort := flag.Int("order-port", 8081, "port for order-service")
	allocPort := flag.Int("allocation-port", 8082, "port for allocation-service")
	riderPort := flag.Int("rider-port", 8083, "port for rider-service")
	requireAuth := flag.Bool("require-auth", false, "require an Authorization: Bearer header on every request")
	flag.Parse()

	world := mock.NewWorld()
	opts := mock.Options{World: world, RequireAuth: *requireAuth}

	servers := []*http.Server{
		newLoggedServer(*orderPort, "order-service", mock.NewOrderService(opts)),
		newLoggedServer(*allocPort, "allocation-service", mock.NewAllocationService(opts)),
		newLoggedServer(*riderPort, "rider-service", mock.NewRiderService(opts)),
	}

	for _, srv := range servers {
		srv := srv
		go func() {
			log.Printf("sapien-fixtures: listening addr=%s", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("sapien-fixtures: server %s failed: %v", srv.Addr, err)
			}
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("sapien-fixtures: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("sapien-fixtures: shutdown error addr=%s: %v", srv.Addr, err)
		}
	}
}

// newLoggedServer wraps handler with brief per-request logging and returns
// an *http.Server bound to the given port.
func newLoggedServer(port int, name string, handler http.Handler) *http.Server {
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		handler.ServeHTTP(w, r)
		log.Printf("%s %s %s %s", name, r.Method, r.URL.Path, time.Since(start))
	})
	return &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: logged,
	}
}
