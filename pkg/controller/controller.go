package controller

import (
	"context"
	"errors"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	mysqlalpha1 "github.com/cyhw/mysql-operator/pkg/apis/mysql/v1alpha1"
	crclientset "github.com/cyhw/mysql-operator/pkg/clients/clientset/versioned"
	crinformer "github.com/cyhw/mysql-operator/pkg/clients/informers/externalversions/mysql/v1alpha1"
)

var (
	matchLabelKey                 = "app"
	matchLabelVal                 = "mysql"
	serviceName                   = "mysql"
	replicas                      = int32(1)
	terminationGracePeriodSeconds = int64(10)
	containerName                 = "mysql"
	imagePrefix                   = "arm64v8/mysql:"
	volumeMountName               = "mysql-store"
	volumeMoutPath                = "/var/lib/mysql"
	envName                       = "MYSQL_ROOT_PASSWORD"
	secretName                    = "mysql-password"
	passwd                        = "bytedance"
	port                          = int32(3306)
)

type Controller struct {
	k8sClient kubernetes.Interface
	crClient  crclientset.Interface
	crSynced  cache.InformerSynced
}

func NewController(k8sClient kubernetes.Interface, crClient crclientset.Interface, crInformer crinformer.MySQLInformer) *Controller {
	controller := &Controller{
		k8sClient: k8sClient,
		crClient:  crClient,
		crSynced:  crInformer.Informer().HasSynced,
	}

	klog.InfoS("Set up event handlers.")
	crInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.add,
		UpdateFunc: controller.update,
		DeleteFunc: controller.delete,
	})

	return controller
}

func (c *Controller) Run(stopCh <-chan struct{}) error {
	klog.InfoS("Run controller.")

	klog.InfoS("Wait for informer cache to sync.")
	if ok := cache.WaitForCacheSync(stopCh, c.crSynced); !ok {
		return errors.New("Failed to wait for caches to sync.")
	}

	klog.InfoS("Start worker.")
	<-stopCh
	klog.InfoS("Shut down.")

	return nil
}

func (c *Controller) add(obj interface{}) {
	klog.InfoS("Receive ADD Event.")

	mysqlObj, ok := obj.(*mysqlalpha1.MySQL)
	if !ok {
		klog.Errorf("Failed to type assert object: %v", obj)
		return
	}
	klog.InfoS("obj", "namespace", mysqlObj.Namespace, "name", mysqlObj.Name, "version", mysqlObj.Spec.Version)

	ret := mysqlObj.DeepCopy()
	ret.Status.Message = "Received In ADD"
	_, err := c.crClient.VolcV1alpha1().MySQLs(ret.Namespace).UpdateStatus(context.TODO(), ret, metav1.UpdateOptions{})
	if err != nil {
		klog.ErrorS(err, "Failed to update status", "namespace", ret.Namespace, "name", ret.Name)
		return
	}
	klog.InfoS("Update Status.", "namespace", ret.Namespace, "name", ret.Name, "version", mysqlObj.Spec.Version)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			envName: passwd,
		},
	}
	_, err = c.k8sClient.CoreV1().Secrets(ret.Namespace).Create(context.Background(), secret, metav1.CreateOptions{})
	if err != nil {
		ret.Status.Message = "Failed"
		_, err = c.crClient.VolcV1alpha1().MySQLs(ret.Namespace).UpdateStatus(context.TODO(), ret, metav1.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to update status", "namespace", ret.Namespace, "name", ret.Name)
			return
		}
		klog.ErrorS(err, "Failed to create secret", "namespace", ret.Namespace, "name", secretName)
		return
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: serviceName,
			Labels: map[string]string{
				matchLabelKey: matchLabelVal,
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port: port,
				},
			},
			ClusterIP: "None",
			Selector: map[string]string{
				matchLabelKey: matchLabelVal,
			},
		},
	}
	_, err = c.k8sClient.CoreV1().Services(ret.Namespace).Create(context.Background(), service, metav1.CreateOptions{})
	if err != nil {
		ret.Status.Message = "Failed"
		_, err = c.crClient.VolcV1alpha1().MySQLs(ret.Namespace).UpdateStatus(context.TODO(), ret, metav1.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to update status", "namespace", ret.Namespace, "name", ret.Name)
			return
		}
		_ = c.k8sClient.CoreV1().Secrets(ret.Namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
		klog.ErrorS(err, "Failed to create service", "namespace", ret.Namespace, "name", ret.Name)
		return
	}

	podTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				matchLabelKey: matchLabelVal,
			},
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: &terminationGracePeriodSeconds,
			Containers: []corev1.Container{
				{
					Name:  containerName,
					Image: imagePrefix + ret.Spec.Version,
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: port,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      volumeMountName,
							MountPath: volumeMoutPath,
						},
					},
					Env: []corev1.EnvVar{
						{
							Name: envName,
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: secretName,
									},
									Key: envName,
								},
							},
						},
					},
				},
			},
		},
	}

	vcTemplate := []corev1.PersistentVolumeClaim{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: volumeMountName,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: {
							Format: "1Gi",
						},
					},
					Limits: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceStorage: {
							Format: "2Gi",
						},
					},
				},
			},
		},
	}

	sts := &v1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ret.Name + "-deployment",
			Namespace: ret.Namespace,
		},
		Spec: v1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					matchLabelKey: matchLabelVal,
				},
			},
			ServiceName:          serviceName,
			Replicas:             &replicas,
			Template:             podTemplate,
			VolumeClaimTemplates: vcTemplate,
		},
	}
	_, err = c.k8sClient.AppsV1().StatefulSets(ret.Namespace).Create(context.Background(), sts, metav1.CreateOptions{})
	if err != nil {
		ret.Status.Message = "Failed"
		_, err = c.crClient.VolcV1alpha1().MySQLs(ret.Namespace).UpdateStatus(context.TODO(), ret, metav1.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to update status", "namespace", ret.Namespace, "name", ret.Name)
			return
		}
		_ = c.k8sClient.CoreV1().Secrets(ret.Namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
		_ = c.k8sClient.CoreV1().Services(ret.Namespace).Delete(context.Background(), serviceName, metav1.DeleteOptions{})
	}
}

func (c *Controller) update(old, new interface{}) {
	klog.InfoS("Receive UPDATE Event.")

	oldObj, ok := old.(*mysqlalpha1.MySQL)
	if !ok {
		klog.Errorf("Failed to type assert old: %v", oldObj)
		return
	}
	klog.InfoS("old", "namespace", oldObj.Namespace, "name", oldObj.Name, "version", oldObj.Spec.Version)

	newObj, ok := new.(*mysqlalpha1.MySQL)
	if !ok {
		klog.Errorf("Failed to type assert new: %v", newObj)
		return
	}
	klog.InfoS("new", "namespace", newObj.Namespace, "name", newObj.Name, "version", newObj.Spec.Version)
}

func (c *Controller) delete(obj interface{}) {
	klog.InfoS("Receive DELETE Event.")

	mysqlObj, ok := obj.(*mysqlalpha1.MySQL)
	if !ok {
		klog.Errorf("Failed to type assert object: %v", obj)
		return
	}
	klog.InfoS("obj", "namespace", mysqlObj.Namespace, "name", mysqlObj.Name, "version", mysqlObj.Spec.Version)

	_ = c.crClient.VolcV1alpha1().MySQLs(mysqlObj.Namespace).Delete(context.TODO(), mysqlObj.Name, metav1.DeleteOptions{})
	_ = c.k8sClient.CoreV1().Secrets(mysqlObj.Namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
	_ = c.k8sClient.CoreV1().Services(mysqlObj.Namespace).Delete(context.Background(), serviceName, metav1.DeleteOptions{})
	_ = c.k8sClient.AppsV1().StatefulSets(mysqlObj.Namespace).Delete(context.Background(), mysqlObj.Name+"-deployment", metav1.DeleteOptions{})
}
