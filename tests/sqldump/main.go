package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/aegion-dynamic/graphjin-slim/core/v3"
	_ "github.com/aegion-dynamic/graphjin-slim/graphql/v3"
	postgresmod "github.com/aegion-dynamic/graphjin-slim/postgres/v3"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	dsn := os.Getenv("GJ_TEST_PG")
	db, err := sql.Open(postgresmod.DriverPostgres, dsn)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	conf := core.Config{DisableAllowList: true}
	gj, err := core.NewGraphJin(&conf, db)
	if err != nil {
		panic(err)
	}
	defer gj.Close()
	query := os.Args[1]
	var vars []byte
	if len(os.Args) > 2 {
		vars = []byte(os.Args[2])
	}
	// A transaction that is rolled back, so a probe never changes the seed.
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	defer tx.Rollback()
	res, err := gj.GraphQL(context.Background(), query, vars, &core.RequestConfig{Tx: tx})
	if err != nil {
		fmt.Println("ERR:", err)
		if res != nil {
			fmt.Println("SQL:", res.SQL())
		}
		return
	}
	fmt.Println("DATA:", string(res.Data))
	fmt.Println("SQL:", res.SQL())
}
