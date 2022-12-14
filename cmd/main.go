package main

import (
	"context"
	"flag"
	"k8s.io/client-go/kubernetes"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	crclientset "github.com/cyhw/mysql-operator/pkg/clients/clientset/versioned"
	crinformer "github.com/cyhw/mysql-operator/pkg/clients/informers/externalversions"
	crcontroller "github.com/cyhw/mysql-operator/pkg/controller"
)

var kubeconfig string

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "filepath to the kubeconfig file")
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	var cfg *rest.Config
	var err error
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		klog.Fatalf("Failed to build kubeconfig: %s", err)
	}

	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Failed to build k8s client: %s", err)
	}

	crClient, err := crclientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Failed to build custom resource client: %s", err)
	}

	crInformerFactory := crinformer.NewSharedInformerFactory(crClient, 0)
	ctrl := crcontroller.NewController(k8sClient, crClient, crInformerFactory.Volc().V1alpha1().MySQLs())

	ctx := context.TODO()
	crInformerFactory.Start(ctx.Done())

	err = ctrl.Run(ctx.Done())
	if err != nil {
		klog.Fatalf("Failed to run controller: %s", err)
	}
	klog.InfoS("Exit.")
}
