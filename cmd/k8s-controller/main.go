// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"flag"
	"net/http"
	"time"

	api "github.com/pliu/k8s-controller/api/v1alpha1"
	c "github.com/pliu/k8s-controller/internal/controller"
	s "github.com/pliu/k8s-controller/internal/server"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	// The reconciler always runs; the viewer is opt-in, so the unauthenticated
	// HTTP surface is never exposed by accident.
	runServer := flag.Bool("server", false, "additionally serve the read-only HTTP API and UI on -listen")
	// Leader election needs a namespace to hold its lease, which the process can
	// only discover from inside the cluster. A single process run against a
	// kubeconfig has nothing to contend with anyway, so it turns off there.
	noLeaderElect := flag.Bool("no-leader-election", false, "skip leader election, which a single process running outside the cluster cannot set up and does not need")
	syncPeriod := flag.Duration("sync-period", time.Hour, "interval at which the reconciler re-syncs every ManagedNamespace as a drift-repair safety net")
	listen := flag.String("listen", ":8080", "address the HTTP server (UI, API, and /metrics) binds; without -server the manager serves /metrics here instead")
	flag.Parse()
	serverEnabled := *runServer

	cfg := ctrl.GetConfigOrDie()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	ctx := ctrl.SetupSignalHandler()

	// Either way the shared metrics registry is served on the listen address:
	// with -server the HTTP server owns that address and serves /metrics itself,
	// so the manager's own listener is disabled; without it the manager's
	// listener binds the address instead.
	metricsAddr := *listen
	if serverEnabled {
		metricsAddr = "0"
	}
	mgr, e := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, LeaderElection: !*noLeaderElect, LeaderElectionID: "k8s-controller.k8s.pliu.dev", HealthProbeBindAddress: ":8081", Cache: cache.Options{SyncPeriod: syncPeriod}, Metrics: metricsserver.Options{BindAddress: metricsAddr}})
	if e != nil {
		panic(e)
	}
	if e = (&c.ManagedNamespaceReconciler{CachedClient: mgr.GetClient(), LiveReader: mgr.GetAPIReader()}).Setup(mgr); e != nil {
		panic(e)
	}
	if e = (&c.ClusterAccessReconciler{CachedClient: mgr.GetClient(), LiveReader: mgr.GetAPIReader()}).Setup(mgr); e != nil {
		panic(e)
	}
	if serverEnabled {
		// Register as a non-leader-election runnable so the API is served by
		// every replica, not only the one holding the reconciler lease. The
		// manager's client already reads both kinds from the informers the
		// reconcilers watch, so the viewer adds no API-server traffic here.
		if e = mgr.Add(&serverRunnable{cl: mgr.GetClient(), addr: *listen}); e != nil {
			panic(e)
		}
	}
	_ = mgr.AddHealthzCheck("healthz", healthz.Ping)
	_ = mgr.AddReadyzCheck("readyz", healthz.Ping)
	if e = mgr.Start(ctx); e != nil {
		panic(e)
	}
}

type serverRunnable struct {
	cl   client.Client
	addr string
}

func (*serverRunnable) NeedLeaderElection() bool { return false }
func (r *serverRunnable) Start(ctx context.Context) error {
	return serve(ctx, r.cl, r.addr)
}

func serve(ctx context.Context, cl client.Client, addr string) error {
	app := &s.Server{Client: cl}
	httpServer := &http.Server{Addr: addr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(sctx)
	}()
	if e := httpServer.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		return e
	}
	return nil
}
