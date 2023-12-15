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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
)

// SecretReconciler reconciles a Secret object
type SecretReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=secrets/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).V(2)

	// Retrieve all secrets
	allSecrets := &corev1.SecretList{}
	err := r.List(ctx, allSecrets)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Retrieve all namespaces
	allNamespaces := &corev1.NamespaceList{}
	err = r.List(ctx, allNamespaces)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Find existing source secrets
	allSourceSecrets := findAllSourceSecrets(allSecrets)
	logger.Info("found source secrets", "count", len(allSourceSecrets))
	// Find existing duplicates
	allDuplicateSecrets := findAllDuplicateSecrets(allSecrets)
	logger.Info("found duplicate secrets", "count", len(allDuplicateSecrets))
	// Filter out namespaces in terminating state because resources in those namespaces cannot be updated
	nonTerminatingNamespaces := findNonTerminatingNamespaces(allNamespaces.Items)
	logger.Info("found non-terminating namespaces", "count", len(nonTerminatingNamespaces))

	// Ensure duplicates exist in all namespaces for all source secrets
	var retryableError error
	logger.Info("Reconciling sources by creating missing duplicates")
	err = r.reconcileSources(ctx, nonTerminatingNamespaces, allSourceSecrets)
	if err != nil {
		retryableError = err
	}

	// Remove orphaned duplicates and update out of sync duplicates
	logger.Info("Reconciling duplicates by removing orphaned duplicates and updating out of sync duplicates")
	err = r.reconcileDuplicates(ctx, allDuplicateSecrets, allSourceSecrets)
	if err != nil {
		retryableError = err
	}

	if retryableError != nil {
		logger.V(1).Error(retryableError, "retrying reconcile with exponential backoff")
	}
	return ctrl.Result{}, retryableError
}

func (r *SecretReconciler) reconcileSources(ctx context.Context, allNamespaces []*corev1.Namespace, allSources []*corev1.Secret) error {
	var retryableError error
	for _, sourceSecret := range allSources {
		// Create missing duplicates
		for _, namespace := range allNamespaces {
			duplicateObjectKey := client.ObjectKey{
				Namespace: namespace.Name,
				Name:      sourceSecret.Name,
			}
			err := r.Get(ctx, duplicateObjectKey, &corev1.Secret{})
			if err != nil {
				if errors.IsNotFound(err) {
					duplicate := newDuplicateSecret(sourceSecret, namespace.Name)
					err = r.Create(ctx, duplicate)
					if err != nil && !errors.IsAlreadyExists(err) {
						retryableError = err
					}
				} else {
					retryableError = err
				}
			}
		}
	}
	return retryableError
}

func (r *SecretReconciler) reconcileDuplicates(ctx context.Context, allDuplicates, allSources []*corev1.Secret) error {
	// Build lookup map for all source secrets
	sourceSecretsMap := make(map[string]*corev1.Secret)
	for _, source := range allSources {
		s := source
		key := client.ObjectKeyFromObject(source).String()
		sourceSecretsMap[key] = s
	}

	var retryableError error

	for _, duplicate := range allDuplicates {
		// annotation must exist because isDuplicateSecret() is used to create the list of duplicates,
		// and it verifies the annotation exists.
		fromAnnotation := duplicate.Annotations[duplicatorFromAnnotationKey]
		sourceSecret, ok := sourceSecretsMap[fromAnnotation]
		if !ok {
			// Delete duplicate if no matching source secret exists
			err := r.Delete(ctx, duplicate)
			if err != nil && !errors.IsNotFound(err) {
				retryableError = err
			}
		} else {
			// Update duplicate when source and duplicate are out of sync
			if !reflect.DeepEqual(duplicate.Data, sourceSecret.Data) {
				updated := newDuplicateSecret(sourceSecret, duplicate.Namespace)
				err := r.Update(ctx, updated)
				if err != nil {
					retryableError = err
				}
			}
		}
	}

	return retryableError
}

func (r *SecretReconciler) triggerFullReconcile(ctx context.Context, obj client.Object) []reconcile.Request {
	// sentinel that means reconcile all secrets (same as if a secret is deleted)
	return []reconcile.Request{
		{
			NamespacedName: client.ObjectKey{
				Namespace: "",
				Name:      "",
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Trigger reconciliation for namespace events too
		Watches(
			&corev1.Namespace{},
			// Reconcile all secrets in all namespaces
			handler.EnqueueRequestsFromMapFunc(r.triggerFullReconcile),
		).
		For(&corev1.Secret{}).
		Complete(r)
}

func findNonTerminatingNamespaces(allNamespaces []corev1.Namespace) []*corev1.Namespace {
	nonTerminatingNamespaces := make([]*corev1.Namespace, 0, len(allNamespaces))
	for _, namespace := range allNamespaces {
		ns := namespace
		if namespace.Status.Phase != corev1.NamespaceTerminating {
			nonTerminatingNamespaces = append(nonTerminatingNamespaces, &ns)
		}
	}
	return nonTerminatingNamespaces
}

func findAllSourceSecrets(allSecrets *corev1.SecretList) []*corev1.Secret {
	sources := make([]*corev1.Secret, 0)
	for _, s := range allSecrets.Items {
		secret := s
		if isSecretDuplicatorSource(&secret) {
			sources = append(sources, &secret)
		}
	}
	return sources
}

func findAllDuplicateSecrets(allSecrets *corev1.SecretList) []*corev1.Secret {
	duplicated := make([]*corev1.Secret, 0)
	for _, s := range allSecrets.Items {
		secret := s
		if isSecretDuplicated(&secret) {
			duplicated = append(duplicated, &secret)
		}
	}
	return duplicated
}

func isSecretDuplicatorSource(secret *corev1.Secret) bool {
	if secret.Annotations == nil {
		return false
	}
	value, ok := secret.Annotations[duplicatorDuplicateAnnotationKey]
	return ok && value == "true"
}

func isSecretDuplicated(secret *corev1.Secret) bool {
	if secret.Annotations == nil {
		return false
	}
	value, ok := secret.Annotations[duplicatorFromAnnotationKey]
	return ok && len(strings.Split(value, "/")) == 2
}

func newDuplicateSecret(source *corev1.Secret, namespace string) *corev1.Secret {
	duplicate := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      source.Name,
			Namespace: namespace,
			// TODO allow adding annotations and labels to duplicates
			Annotations: map[string]string{
				duplicatorFromAnnotationKey: client.ObjectKeyFromObject(source).String(),
			},
		},
		Data: source.Data,
		Type: source.Type,
	}
	return duplicate
}
