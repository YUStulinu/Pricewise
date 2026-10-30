// Command shopsim serves the simulated shops: one port per shop and a control port.
//
//	shopsim -catalogue data/sim/catalogue.json [-admin 127.0.0.1:9100]
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/YUStulinu/Pricewise/collector/internal/sim"
)

func main() {
	path := flag.String("catalogue", "data/sim/catalogue.json", "written by python -m pricewise_ml.simulate")
	admin := flag.String("admin", "127.0.0.1:9100", "address of the simulation's controls")
	fail := flag.Int("fail", 1, "percent of product pages that answer 503 on a given day")
	flag.Parse()

	cat, err := sim.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shopsim:", err)
		os.Exit(1)
	}
	srv, err := sim.NewServer(cat)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shopsim:", err)
		os.Exit(1)
	}
	srv.FailPercent = *fail
	for i, shop := range cat.Shops {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(shop.Port))
		base := "http://" + addr
		go func() {
			if err := http.ListenAndServe(addr, srv.Handler(i, base)); err != nil {
				fmt.Fprintln(os.Stderr, "shopsim:", shop.Name, err)
				os.Exit(1)
			}
		}()
		fmt.Printf("%-20s %s\n", shop.Name, base)
	}
	fmt.Printf("controls             http://%s/_sim/day, /_sim/stats\n", *admin)
	if err := http.ListenAndServe(*admin, srv.Admin()); err != nil {
		fmt.Fprintln(os.Stderr, "shopsim:", err)
		os.Exit(1)
	}
}
