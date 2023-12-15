package controller

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"testing"
)

func Test_findNonTerminatingNamespaces(t *testing.T) {
	expected := []*corev1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ns1",
			},
			Status: corev1.NamespaceStatus{
				Phase: corev1.NamespaceActive,
			},
		},
	}
	input := []corev1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ns1",
			},
			Status: corev1.NamespaceStatus{
				Phase: corev1.NamespaceActive,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ns1",
			},
			Status: corev1.NamespaceStatus{
				Phase: corev1.NamespaceTerminating,
			},
		},
	}

	got := findNonTerminatingNamespaces(input)

	if !reflect.DeepEqual(got, expected) {
		t.Errorf("got %v, wanted %v", got, expected)
	}
}

func Test_findAllSourceSecrets(t *testing.T) {
	expected := []*corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret1",
				Namespace: "ns",
				Annotations: map[string]string{
					duplicatorDuplicateAnnotationKey: "true",
				},
			},
			Type: "Opaque",
			Data: map[string][]byte{
				"username": []byte("admin"),
				"password": []byte("password"),
			},
		},
	}
	input := corev1.SecretList{
		Items: []corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "ns",
					Annotations: map[string]string{
						duplicatorDuplicateAnnotationKey: "true",
					},
				},
				Type: "Opaque",
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("password"),
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "ns",
				},
				Type: "Opaque",
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("password"),
				},
			},
		},
	}

	got := findAllSourceSecrets(&input)

	if !reflect.DeepEqual(got, expected) {
		t.Errorf("got %v, wanted %v", got, expected)
	}
}

func Test_findAllDuplicateSecrets(t *testing.T) {
	expected := []*corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret1",
				Namespace: "ns",
				Annotations: map[string]string{
					duplicatorFromAnnotationKey: "ns/secret1",
				},
			},
			Type: "Opaque",
			Data: map[string][]byte{
				"username": []byte("admin"),
				"password": []byte("password"),
			},
		},
	}
	input := corev1.SecretList{
		Items: []corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "ns",
					Annotations: map[string]string{
						duplicatorFromAnnotationKey: "ns/secret1",
					},
				},
				Type: "Opaque",
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("password"),
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "ns",
				},
				Type: "Opaque",
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("password"),
				},
			},
		},
	}

	got := findAllDuplicateSecrets(&input)

	if !reflect.DeepEqual(got, expected) {
		t.Errorf("got %v, wanted %v", got, expected)
	}
}

func Test_isSecretDuplicatorSource(t *testing.T) {
	testCases := []struct {
		name   string
		secret *corev1.Secret
		want   bool
	}{
		{
			name: "secret is duplicator source",
			want: true,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "ns",
					Annotations: map[string]string{
						duplicatorDuplicateAnnotationKey: "true",
					},
				},
			},
		},
		{
			name: "no annotations",
			want: false,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "ns",
				},
			},
		},
		{
			name: "wrong annotation value",
			want: false,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "ns",
					Annotations: map[string]string{
						duplicatorDuplicateAnnotationKey: "false",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isSecretDuplicatorSource(tc.secret)
			if got != tc.want {
				t.Errorf("got %v, wanted %v", got, tc.want)
			}
		})
	}
}

func Test_isSecretDuplicate(t *testing.T) {
	testCases := []struct {
		name   string
		secret *corev1.Secret
		want   bool
	}{
		{
			name: "secret is duplicator duplicate",
			want: true,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "ns",
					Annotations: map[string]string{
						duplicatorFromAnnotationKey: "another-ns/secret1",
					},
				},
			},
		},
		{
			name: "no annotations",
			want: false,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "ns",
				},
			},
		},
		{
			name: "invalid annotation value",
			want: false,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "ns",
					Annotations: map[string]string{
						duplicatorFromAnnotationKey: "no-slash-in-string",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isSecretDuplicated(tc.secret)
			if got != tc.want {
				t.Errorf("got %v, wanted %v", got, tc.want)
			}
		})
	}
}

func Test_newDuplicateSecret(t *testing.T) {
	namespace := "another-ns"
	input := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret1",
			Namespace: "ns",
			Annotations: map[string]string{
				duplicatorDuplicateAnnotationKey: "true",
			},
		},
	}
	want := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret1",
			Namespace: namespace,
			Annotations: map[string]string{
				duplicatorFromAnnotationKey: "ns/secret1",
			},
		},
	}
	got := newDuplicateSecret(input, namespace)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, wanted %v", got, want)
	}
}
