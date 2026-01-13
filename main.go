package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
)

var (
	ConsumerProjectID  string
	parentFolderID     string
	numTPs             int
	numPoolsEachTP     int
	numVolumesEachPool int
	skipMigrations     bool
)

func main() {
	flag.StringVar(&ConsumerProjectID, "consumer-project", "vsa-billing-09", "Consumer project ID")
	flag.StringVar(&parentFolderID, "parent-folder", "1025659400543", "Parent Folder ID for Tenant Projects")
	flag.IntVar(&numTPs, "num-tps", 1, "Number of Tenant Projects to create")
	flag.IntVar(&numPoolsEachTP, "num-pools", 2, "Number of pools to create Each Tenant Project")
	flag.IntVar(&numVolumesEachPool, "num-volumes", 3, "Number of volumes per pool")
	flag.BoolVar(&skipMigrations, "skip-migrations", false, "Skip running DB migrations pre-flight")
	flag.Parse()

	if skipMigrations {
		// Run DB migrations to ensure schema is up-to-date before inserting mock data
		if err := runMigrations(); err != nil {
			log.Fatalf("Failed to run DB migrations: %v", err)
		}
	}

	fmt.Println("Starting load test simulator with the following parameters:")
	fmt.Printf("Consumer Project ID: %s\n", ConsumerProjectID)
	fmt.Printf("Number of Tenant Projects: %d\n", numTPs)
	fmt.Printf("Number of Pools: %d\n", numPoolsEachTP)
	fmt.Printf("Number of Volumes Each Pool: %d\n", numVolumesEachPool)

	connStrVcp := "host=localhost user=postgres password=testpass dbname=vcp sslmode=disable port=5433"
	vcp, err := openGormConn(connStrVcp)
	if err != nil {
		panic(err)
	}

	accounts, pools, svms, volumes, replications, err := setupResources(vcp)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create metric client: %v", err)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigs
		log.Println("Interrupt received, cleaning up created resources...")
		cleanupAllResources(vcp, volumes, replications, pools, svms, accounts)
		err := client.Close()
		if err != nil {
			return
		}
		cancel()
		os.Exit(0)
	}()

	for i := 0; i < len(volumes); i++ {
		volume := volumes[i]
		replication := replications[i]
		pool := volume.Pool
		go sendMetricsLoop(volume, replication, pool.ClusterDetails.RegionalTenantProject)
	}

	<-ctx.Done()
	err = client.Close()
	if err != nil {
		return
	}
}

// runMigrations invokes the repo's migrate tool to apply the latest DB migrations.
func runMigrations() error {
	// Prepare environment for migrate tool
	envs := os.Environ()
	// Default REGION if not set
	if os.Getenv("REGION") == "" {
		envs = append(envs, "REGION=us-central1")
	}
	cmd := exec.Command("go", "run", "./tools/migrate", "-migrate")
	cmd.Dir = ".." // run from repo root: load-test-simulator -> repo root
	cmd.Env = envs
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
