/*
Copyright 2023 Nick Triller.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"path/filepath"
	"reflect"
	"runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var k8sManagerCancel context.CancelFunc

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	configureGomega()
	RunSpecs(t, "Controller Suite")
}

func configureGomega() {
	SetDefaultEventuallyTimeout(5 * time.Second)
	SetDefaultEventuallyPollingInterval(200 * time.Millisecond)
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel := context.WithCancel(context.Background())
	k8sManagerCancel = cancel

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: false,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.28.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "localhost:8080",
		},
		HealthProbeBindAddress: "localhost:8081",
	})
	Expect(err).ToNot(HaveOccurred())

	err = (&SecretReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	k8sManagerCancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

var _ = Describe("Secret controller", func() {

	const (
		secretNamePrefix = "secret"
		numSecrets       = 3
		sourceNamespace  = "default"
	)

	Context("some source secrets and unrelated secrets", func() {
		var sourceSecrets []*corev1.Secret
		var unrelatedSecrets []*corev1.Secret
		var ctx context.Context

		AfterEach(func() {
			// Delete source secrets first
			// Retrieve all secrets
			allSecrets := &corev1.SecretList{}
			err := k8sClient.List(ctx, allSecrets)
			Expect(err).NotTo(HaveOccurred())
			for _, secret := range allSecrets.Items {
				if isSecretDuplicatorSource(&secret) {
					// Delete source secret
					err := k8sClient.Delete(ctx, &secret)
					if err != nil && !k8sErrors.IsNotFound(err) {
						Expect(err).NotTo(HaveOccurred())
					}
				}
			}
			// Delete all secrets
			allSecrets = &corev1.SecretList{}
			err = k8sClient.List(ctx, allSecrets)
			Expect(err).NotTo(HaveOccurred())
			for _, secret := range allSecrets.Items {
				err := k8sClient.Delete(ctx, &secret)
				if err != nil && !k8sErrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred())
				}
			}
		})

		BeforeEach(func() {
			ctx = context.Background()
			sourceSecrets = make([]*corev1.Secret, 0, numSecrets)
			unrelatedSecrets = make([]*corev1.Secret, 0, numSecrets)

			// Create source secrets
			for i := 0; i < numSecrets; i++ {
				sourceSecret := corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secretNamePrefix + strconv.Itoa(i),
						Namespace: sourceNamespace,
						Annotations: map[string]string{
							duplicatorDuplicateAnnotationKey: "true",
						},
					},
					Data: map[string][]byte{
						"foo": []byte("bar" + strconv.Itoa(i)),
					},
				}
				err := k8sClient.Create(ctx, &sourceSecret)
				if err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}
				sourceSecrets = append(sourceSecrets, &sourceSecret)
			}

			// Create unrelated secrets not managed by duplicator
			for i := 0; i < numSecrets; i++ {
				unrelatedSecret := corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "unrelated" + strconv.Itoa(i),
						Namespace: sourceNamespace,
					},
					Data: map[string][]byte{
						"fooofoo": []byte("barbar" + strconv.Itoa(i)),
					},
				}
				err := k8sClient.Create(ctx, &unrelatedSecret)
				if err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}
				unrelatedSecrets = append(unrelatedSecrets, &unrelatedSecret)
			}

			// Create a few extra namespaces
			for i := 0; i < 5; i++ {
				namespace := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns-" + strconv.Itoa(i),
					},
				}
				err := k8sClient.Create(ctx, namespace)
				if err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}
			}

			Eventually(assertDuplicatesExistAndMatchSourceSecrets(ctx, sourceSecrets)).Should(Succeed())
		})

		It("should create a duplicate secret in each namespace", func() {
			Eventually(assertDuplicatesExistAndMatchSourceSecrets(ctx, sourceSecrets)).Should(Succeed())
			Expect(assertUnrelatedSecretsUnchanged(ctx, unrelatedSecrets)()).To(Succeed())
		})

		It("should copy existing duplicator secrets to new namespace", func() {
			// create a new namespace
			newNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-ns",
				},
			}
			err := k8sClient.Create(ctx, newNamespace)
			Expect(err).NotTo(HaveOccurred())
			Eventually(assertDuplicatesExistAndMatchSourceSecrets(ctx, sourceSecrets)).Should(Succeed())
			Expect(assertUnrelatedSecretsUnchanged(ctx, unrelatedSecrets)()).To(Succeed())
			// Keep NS as deleting namespaces is not supported,
			// see https://book.kubebuilder.io/reference/envtest.html#namespace-usage-limitation
		})

		It("should revert change to duplicate", func() {
			// Update duplicate
			ns := "kube-system"
			modifiedDataKey := "modified"
			updatedDuplicate := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sourceSecrets[0].Name,
					Namespace: ns,
					Annotations: map[string]string{
						duplicatorFromAnnotationKey: client.ObjectKeyFromObject(sourceSecrets[0]).String(),
					},
				},
				Data: map[string][]byte{modifiedDataKey: []byte("val")},
			}
			err := k8sClient.Update(ctx, updatedDuplicate)
			Expect(err).NotTo(HaveOccurred())
			Eventually(assertDuplicatesExistAndMatchSourceSecrets(ctx, sourceSecrets)).Should(Succeed())
			Expect(assertUnrelatedSecretsUnchanged(ctx, unrelatedSecrets)()).To(Succeed())
		})

		It("should not clobber existing secret with duplicate or delete unrelated secret", func() {
			// Create an unrelated secret with same name as duplicate will have
			secretName := "i-am-a-secret"
			ns := "kube-system"
			sourceNs := "default"
			unrelatedSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: ns,
				},
				Data: map[string][]byte{
					"asd": []byte("asd"),
				},
			}
			err := k8sClient.Create(ctx, unrelatedSecret)
			Expect(err).NotTo(HaveOccurred())
			// Create source secret with same name
			sourceSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: sourceNs,
					Annotations: map[string]string{
						duplicatorDuplicateAnnotationKey: "true",
					},
				},
				Data: map[string][]byte{
					"q": []byte("p"),
				},
			}
			err = k8sClient.Create(ctx, sourceSecret)
			Expect(err).NotTo(HaveOccurred())
			// Verify unrelated secret is unchanged
			time.Sleep(1 * time.Second)
			gotSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(unrelatedSecret), gotSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(gotSecret.Data).To(Equal(unrelatedSecret.Data))
			// Delete source secret
			err = k8sClient.Delete(ctx, sourceSecret)
			Expect(err).NotTo(HaveOccurred())
			// Verify unrelated secret is unchanged
			time.Sleep(1 * time.Second)
			gotSecret = &corev1.Secret{}
			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(unrelatedSecret), gotSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(gotSecret.Data).To(Equal(unrelatedSecret.Data))
		})

		It("should update duplicates when source secret changes", func() {
			updatedSource := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sourceSecrets[0].Name,
					Namespace: sourceSecrets[0].Namespace,
					Annotations: map[string]string{
						duplicatorDuplicateAnnotationKey: "true",
					},
				},
				Data: map[string][]byte{"zzz": []byte("xxx")},
			}
			// Update one source secret
			err := k8sClient.Update(ctx, updatedSource)
			Expect(err).NotTo(HaveOccurred())
			sourceSecretsUpdated := make([]*corev1.Secret, len(sourceSecrets))
			copy(sourceSecretsUpdated, sourceSecrets)
			sourceSecretsUpdated[0] = updatedSource
			Eventually(assertDuplicatesExistAndMatchSourceSecrets(ctx, sourceSecretsUpdated)).Should(Succeed())
			Expect(assertUnrelatedSecretsUnchanged(ctx, unrelatedSecrets)()).To(Succeed())
		})

		It("should delete duplicates when source secret is deleted", func() {
			// Delete one source secret
			deletedSecret := sourceSecrets[0]
			err := k8sClient.Delete(ctx, deletedSecret)
			Expect(err).NotTo(HaveOccurred())
			// Check that duplicates are deleted in all namespaces
			namespaces := &corev1.NamespaceList{}
			err = k8sClient.List(ctx, namespaces)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() error {
				for _, namespace := range namespaces.Items {
					secret := &corev1.Secret{}
					err = k8sClient.Get(ctx, client.ObjectKey{
						Namespace: namespace.Name,
						Name:      sourceSecrets[0].Name,
					}, secret)
					if err != nil && k8sErrors.IsNotFound(err) {
						return nil
					} else {
						return fmt.Errorf("secret %s/%s still exists", namespace.Name, sourceSecrets[0].Name)
					}
				}
				return nil
			}).Should(Succeed())
			sourceSecretsDeleted := make([]*corev1.Secret, len(sourceSecrets)-1)
			copy(sourceSecretsDeleted, sourceSecrets[1:])
			Eventually(assertDuplicatesExistAndMatchSourceSecrets(ctx, sourceSecretsDeleted)).Should(Succeed())
			Expect(assertUnrelatedSecretsUnchanged(ctx, unrelatedSecrets)()).To(Succeed())
			Eventually(assertNoDuplicatesFor(ctx, deletedSecret)).Should(Succeed())
		})

		It("should not block because of two source secrets in different namespaces with same name", func() {
			duplicateSource := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sourceSecrets[0].Name,
					Namespace: "kube-system",
					Annotations: map[string]string{
						duplicatorDuplicateAnnotationKey: "true",
					},
				},
				Data: map[string][]byte{"foo": []byte("bar0")},
			}
			// Update duplicate to be source secret
			err := k8sClient.Update(ctx, duplicateSource)
			Expect(err).NotTo(HaveOccurred())

			// Create new source secret
			newSource := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-source",
					Namespace: "default",
					Annotations: map[string]string{
						duplicatorDuplicateAnnotationKey: "true",
					},
				},
				Data: map[string][]byte{"zzz": []byte("xxx")},
			}
			err = k8sClient.Create(ctx, newSource)
			Expect(err).NotTo(HaveOccurred())
			// Verify new source secret is synced across namespaces
			sourceSecretsModified := make([]*corev1.Secret, len(sourceSecrets)-1, len(sourceSecrets))
			copy(sourceSecretsModified, sourceSecrets[1:])
			sourceSecretsModified = append(sourceSecretsModified, newSource)
			Eventually(assertDuplicatesExistAndMatchSourceSecrets(ctx, sourceSecretsModified)).Should(Succeed())
			Expect(assertUnrelatedSecretsUnchanged(ctx, unrelatedSecrets)()).To(Succeed())
		})

		It("should not block sync when a duplicate secret has malformed annotation", func() {
			updatedDuplicate := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sourceSecrets[0].Name,
					Namespace: "kube-system",
					Annotations: map[string]string{
						duplicatorFromAnnotationKey: "malformed-no-slash",
					},
				},
				Data: map[string][]byte{"zzz": []byte("xxx")},
			}
			// Update duplicate secret
			err := k8sClient.Update(ctx, updatedDuplicate)
			Expect(err).NotTo(HaveOccurred())

			sourceSecretsModified := make([]*corev1.Secret, len(sourceSecrets)-1)
			copy(sourceSecretsModified, sourceSecrets[1:])

			// Create a new source secret
			newSource := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-source",
					Namespace: "default",
					Annotations: map[string]string{
						duplicatorDuplicateAnnotationKey: "true",
					},
				},
			}
			err = k8sClient.Create(ctx, newSource)
			Expect(err).NotTo(HaveOccurred())
			sourceSecretsModified = append(sourceSecretsModified, newSource)

			Eventually(assertDuplicatesExistAndMatchSourceSecrets(ctx, sourceSecretsModified)).Should(Succeed())
			Expect(assertUnrelatedSecretsUnchanged(ctx, unrelatedSecrets)()).To(Succeed())
		})

		It("should delete duplicates when source secret annotation is removed", func() {
			// Remove duplicate=true annotation
			modifiedDataKey := "modified"
			updatedSource := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:        sourceSecrets[0].Name,
					Namespace:   sourceSecrets[0].Namespace,
					Annotations: map[string]string{},
				},
				Data: map[string][]byte{modifiedDataKey: []byte("val")},
			}
			err := k8sClient.Update(ctx, updatedSource)
			Expect(err).NotTo(HaveOccurred())

			sourceSecretsUpdated := make([]*corev1.Secret, 0, len(sourceSecrets)-1)
			copy(sourceSecretsUpdated, sourceSecrets[1:])

			Eventually(assertDuplicatesExistAndMatchSourceSecrets(ctx, sourceSecretsUpdated)).Should(Succeed())
			Expect(assertUnrelatedSecretsUnchanged(ctx, unrelatedSecrets)()).To(Succeed())
			Eventually(assertNoDuplicatesFor(ctx, updatedSource)).Should(Succeed())
		})
	})
})

func assertUnrelatedSecretsUnchanged(ctx context.Context, unrelatedSecrets []*corev1.Secret) func() error {
	return func() error {
		// Verify unrelated secrets haven't been changed
		for _, unrelatedSecret := range unrelatedSecrets {
			gotSecret := &corev1.Secret{}
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(unrelatedSecret), gotSecret)
			if err != nil {
				return err
			}
			Expect(unrelatedSecret.Data).To(Equal(gotSecret.Data))
			Expect(unrelatedSecret.Annotations).To(Equal(gotSecret.Annotations))
			Expect(unrelatedSecret.Labels).To(Equal(gotSecret.Labels))
		}
		return nil
	}
}

func assertDuplicatesExistAndMatchSourceSecrets(ctx context.Context, sourceSecrets []*corev1.Secret) func() error {
	return func() error {
		// Retrieve all namespaces
		allNamespaces := &corev1.NamespaceList{}
		err := k8sClient.List(ctx, allNamespaces)
		if err != nil {
			return err
		}
		// Verify copy exists in all namespaces
		for _, sourceSecret := range sourceSecrets {
			for _, namespace := range allNamespaces.Items {
				if sourceSecret.Namespace == namespace.Name {
					gotSourceSecret := &corev1.Secret{}
					err = k8sClient.Get(ctx, client.ObjectKeyFromObject(sourceSecret), gotSourceSecret)
					if err != nil {
						return errors.Wrap(err, fmt.Sprintf("failed to get source secret %s/%s",
							sourceSecret.Namespace, sourceSecret.Name))
					}
					// Compare data
					if !reflect.DeepEqual(gotSourceSecret.Data, sourceSecret.Data) {
						return fmt.Errorf("source secret %s/%s has changed. Got: %s, wanted %s",
							gotSourceSecret.Namespace, gotSourceSecret.Name, gotSourceSecret.Data, sourceSecret.Data)
					}
					// Verify from annotation
					if gotSourceSecret.Annotations == nil {
						return fmt.Errorf("source secret %s/%s has no annotations", gotSourceSecret.Namespace, gotSourceSecret.Name)
					}
					if val, ok := gotSourceSecret.Annotations[duplicatorDuplicateAnnotationKey]; !ok || val != "true" {
						return fmt.Errorf("source secret %s/%s has no annotation %s=true",
							gotSourceSecret.Namespace, gotSourceSecret.Name, duplicatorDuplicateAnnotationKey)
					}
					continue
				}
				duplicateObjectKey := client.ObjectKey{
					Namespace: namespace.Name,
					Name:      sourceSecret.Name,
				}
				gotSecret := &corev1.Secret{}
				err = k8sClient.Get(ctx, duplicateObjectKey, gotSecret)
				if err != nil {
					return errors.Wrap(err, fmt.Sprintf("failed to get duplicate secret %s", duplicateObjectKey.String()))
				}
				// Verify content
				if !reflect.DeepEqual(gotSecret.Data, sourceSecret.Data) {
					return fmt.Errorf("duplicate secret %s/%s does not match source secret %s/%s. Got: %s, wanted %s",
						gotSecret.Namespace, gotSecret.Name, sourceSecret.Namespace, sourceSecret.Name, gotSecret.Data, sourceSecret.Data)
				}
				// Verify annotations
				Expect(gotSecret.Annotations).To(
					HaveKeyWithValue(duplicatorFromAnnotationKey, sourceSecret.Namespace+"/"+sourceSecret.Name),
				)
			}
		}
		return nil
	}
}

func assertNoDuplicatesFor(ctx context.Context, secret *corev1.Secret) func() error {
	return func() error {
		// Retrieve all namespaces
		allNamespaces := &corev1.NamespaceList{}
		err := k8sClient.List(ctx, allNamespaces)
		if err != nil {
			return err
		}
		for _, namespace := range allNamespaces.Items {
			if namespace.Name == secret.Namespace {
				continue
			}
			duplicateObjectKey := client.ObjectKey{
				Namespace: namespace.Name,
				Name:      secret.Name,
			}
			gotSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, duplicateObjectKey, gotSecret)
			if err != nil {
				if k8sErrors.IsNotFound(err) {
					continue
				}
				return errors.Wrap(err, fmt.Sprintf("failed to get duplicate secret %s", duplicateObjectKey.String()))
			}
			// Verify the secret is not marked as duplicate, i.e. it's an unrelated secret with same name
			if gotSecret.Annotations == nil {
				continue
			}
			if val, ok := gotSecret.Annotations[duplicatorFromAnnotationKey]; ok {
				return fmt.Errorf("expected unmanaged secret but secret %s has annotation %s=%s",
					duplicateObjectKey.String(), duplicatorFromAnnotationKey, val)
			}
		}
		return nil
	}
}
