// Copyright 2020 The Shipwright Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	coreinf "k8s.io/client-go/informers"
	corecli "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/shipwright-io/image/controllers"
	iimgcli "github.com/shipwright-io/image/infra/images/v1beta1/gen/clientset/versioned"
	iimginf "github.com/shipwright-io/image/infra/images/v1beta1/gen/informers/externalversions"
	"github.com/shipwright-io/image/infra/starter"
	"github.com/shipwright-io/image/services"
)

// Version holds the current binary version. Set at compile time.
var Version = "v0.0.0"

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	ctx, stop := signal.NotifyContext(
		context.Background(), syscall.SIGTERM, syscall.SIGINT,
	)
	go func() {
		<-ctx.Done()
		stop()
	}()

	klog.Info(`starting shipwright image controller...`)
	klog.Info(`version `, Version)

	kubeconfig := os.Getenv("KUBECONFIG")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		klog.Fatalf("unable to read kubeconfig: %v", err)
	}

	// creates image client and informer.
	imgcli, err := iimgcli.NewForConfig(config)
	if err != nil {
		log.Fatalf("unable to create image image client: %v", err)
	}
	imginf := iimginf.NewSharedInformerFactory(imgcli, time.Minute)

	// creates core client and informer.
	corcli, err := corecli.NewForConfig(config)
	if err != nil {
		log.Fatalf("unable to create core client: %v", err)
	}
	corinf := coreinf.NewSharedInformerFactory(corcli, time.Minute)

	// create our service layer
	impsvc := services.NewImageImport(corinf, imgcli, imginf)
	imgsvc := services.NewImage(corinf, imgcli, imginf)
	tiosvc := services.NewImageIO(corinf, imgcli, imginf)
	usrsvc := services.NewUser(corcli)

	// create controller layer
	imctrl := controllers.NewImageImport(impsvc)
	itctrl := controllers.NewImage(imgsvc)
	mtctrl := controllers.NewMutatingWebHook(impsvc, imgsvc)
	tioctr := controllers.NewImageIO(tiosvc, usrsvc)
	moctrl := controllers.NewMetric()

	// starts up all informers and waits for their cache to sync up,
	// only then we start the controllers i.e. start to process events
	// from the queue.
	klog.Info("waiting for caches to sync ...")
	corinf.Start(ctx.Done())
	imginf.Start(ctx.Done())
	if !cache.WaitForCacheSync(
		ctx.Done(),
		corinf.Core().V1().ConfigMaps().Informer().HasSynced,
		corinf.Core().V1().Secrets().Informer().HasSynced,
		imginf.Shipwright().V1beta1().Images().Informer().HasSynced,
		imginf.Shipwright().V1beta1().ImageImports().Informer().HasSynced,
	) {
		klog.Fatal("caches not syncing")
	}
	klog.Info("caches in sync, moving on.")

	st := starter.New(corcli, mtctrl, itctrl, moctrl, tioctr, imctrl)
	if err := st.Start(ctx, "imgctrl-leader-election"); err != nil {
		klog.Errorf("unable to start controllers: %s", err)
	}
}
