package k8s

import (
	"context"
	"log"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/rest"
        
        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func RunLeaderElection(ctx context.Context, id string, run func(ctx context.Context)) error {

	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	client := kubernetes.NewForConfigOrDie(config)

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "workspace-sync-leader",
			Namespace: os.Getenv("POD_NAMESPACE"),
		},
		Client: client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: id,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,

		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				log.Println("Became leader")
				run(ctx)
			},
			OnStoppedLeading: func() {
				log.Println("Lost leadership")
			},
		},
	})

	return nil
}
