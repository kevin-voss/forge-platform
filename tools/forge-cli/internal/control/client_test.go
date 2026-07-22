package control

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProjectMethodsUseControlContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-Id", "request-123")
		switch request.URL.Path {
		case "/v1/projects":
			if request.Method == http.MethodPost {
				body, _ := io.ReadAll(request.Body)
				if string(body) != `{"name":"acme","slug":"acme"}` {
					t.Errorf("create body = %s", body)
				}
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(`{"id":"project-1","name":"acme","slug":"acme","createdAt":"now","updatedAt":"now"}`))
				return
			}
			_, _ = writer.Write([]byte(`[{"id":"project-1","name":"acme","slug":"acme","createdAt":"now","updatedAt":"now"}]`))
		case "/v1/projects/project-1":
			_, _ = writer.Write([]byte(`{"id":"project-1","name":"acme","slug":"acme","createdAt":"now","updatedAt":"now"}`))
		case "/v1/projects/project-1/environments":
			if request.Method == http.MethodPost {
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(`{"id":"env-1","projectId":"project-1","name":"dev","createdAt":"now","updatedAt":"now"}`))
				return
			}
			_, _ = writer.Write([]byte(`[{"id":"env-1","projectId":"project-1","name":"dev","createdAt":"now","updatedAt":"now"}]`))
		case "/v1/projects/project-1/applications":
			if request.Method == http.MethodPost {
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(`{"id":"app-1","projectId":"project-1","name":"web","createdAt":"now","updatedAt":"now"}`))
				return
			}
			_, _ = writer.Write([]byte(`[{"id":"app-1","projectId":"project-1","name":"web","createdAt":"now","updatedAt":"now"}]`))
		case "/v1/applications/app-1/services":
			if request.Method == http.MethodPost {
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(`{"id":"service-1","applicationId":"app-1","name":"api","port":8080,"createdAt":"now","updatedAt":"now"}`))
				return
			}
			_, _ = writer.Write([]byte(`[{"id":"service-1","applicationId":"app-1","name":"api","port":8080,"createdAt":"now","updatedAt":"now"}]`))
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	var diagnostics []string
	client, err := New(server.URL, time.Second, func(method, path string, status int, requestID string, _ time.Duration) {
		diagnostics = append(diagnostics, method+" "+path+" "+requestID)
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	created, err := client.CreateProject(context.Background(), "acme", "acme")
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if created.ID != "project-1" || created.Slug != "acme" {
		t.Fatalf("created project = %#v", created)
	}
	projects, err := client.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "acme" {
		t.Fatalf("projects = %#v", projects)
	}
	if _, err := client.GetProject(context.Background(), "project-1"); err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if _, err := client.CreateEnvironment(context.Background(), "project-1", "dev"); err != nil {
		t.Fatalf("CreateEnvironment() error = %v", err)
	}
	if environments, err := client.ListEnvironments(context.Background(), "project-1"); err != nil || len(environments) != 1 {
		t.Fatalf("ListEnvironments() = %#v, %v", environments, err)
	}
	if _, err := client.CreateApplication(context.Background(), "project-1", "web"); err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}
	if applications, err := client.ListApplications(context.Background(), "project-1"); err != nil || len(applications) != 1 {
		t.Fatalf("ListApplications() = %#v, %v", applications, err)
	}
	if _, err := client.CreateService(context.Background(), "app-1", "api", 8080); err != nil {
		t.Fatalf("CreateService() error = %v", err)
	}
	if services, err := client.ListServices(context.Background(), "app-1"); err != nil || len(services) != 1 {
		t.Fatalf("ListServices() = %#v, %v", services, err)
	}
	if len(diagnostics) != 9 || !strings.Contains(diagnostics[0], "request-123") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestControlErrorEnvelopeIncludesRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-Id", "header-request-id")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"error":{"code":"PROJECT_NOT_FOUND","message":"project not found","requestId":"body-request-id"}}`))
	}))
	defer server.Close()

	client, err := New(server.URL, time.Second, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.GetProject(context.Background(), "missing")
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %v, want APIError", err)
	}
	if apiError.Code != "PROJECT_NOT_FOUND" || apiError.RequestID != "body-request-id" {
		t.Fatalf("APIError = %#v", apiError)
	}
	if got := apiError.Error(); !strings.Contains(got, "requestId: body-request-id") {
		t.Fatalf("error message = %q", got)
	}
}

func TestDeploymentMethodsUseControlContract(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/services/service-1/deployments":
			if request.Method == http.MethodPost {
				requests++
				if got := request.Header.Get("Idempotency-Key"); got != "retry-key" {
					t.Errorf("Idempotency-Key = %q, want retry-key", got)
				}
				body, _ := io.ReadAll(request.Body)
				if got, want := string(body), `{"image":"registry.example/api:1","desiredReplicas":2,"environmentId":"env-1"}`; got != want {
					t.Errorf("create body = %s, want %s", got, want)
				}
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(`{"id":"deployment-1","serviceId":"service-1","environmentId":"env-1","image":"registry.example/api:1","desiredReplicas":2,"status":"pending","createdAt":"now","updatedAt":"now"}`))
				return
			}
			_, _ = writer.Write([]byte(`[{"id":"deployment-1","serviceId":"service-1","environmentId":"env-1","image":"registry.example/api:1","desiredReplicas":2,"status":"pending","createdAt":"now","updatedAt":"now"}]`))
		case "/v1/deployments/deployment-1":
			_, _ = writer.Write([]byte(`{"id":"deployment-1","serviceId":"service-1","environmentId":"env-1","image":"registry.example/api:1","desiredReplicas":2,"status":"pending","createdAt":"now","updatedAt":"now"}`))
		case "/v1/deployments/missing":
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"error":{"code":"DEPLOYMENT_NOT_FOUND","message":"deployment not found","requestId":"request-404"}}`))
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, time.Second, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	created, err := client.CreateDeployment(context.Background(), "service-1", "registry.example/api:1", 2, "env-1", "retry-key")
	if err != nil || created.Status != "pending" {
		t.Fatalf("CreateDeployment() = %#v, %v", created, err)
	}
	if _, err := client.CreateDeployment(context.Background(), "service-1", "registry.example/api:1", 2, "env-1", "retry-key"); err != nil {
		t.Fatalf("idempotent replay = %v", err)
	}
	if requests != 2 {
		t.Fatalf("create requests = %d, want 2", requests)
	}
	if deployment, err := client.GetDeployment(context.Background(), "deployment-1"); err != nil || deployment.ID != "deployment-1" {
		t.Fatalf("GetDeployment() = %#v, %v", deployment, err)
	}
	if deployments, err := client.ListDeployments(context.Background(), "service-1"); err != nil || len(deployments) != 1 {
		t.Fatalf("ListDeployments() = %#v, %v", deployments, err)
	}
	_, err = client.GetDeployment(context.Background(), "missing")
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Status != http.StatusNotFound {
		t.Fatalf("GetDeployment(missing) error = %#v, want 404 APIError", err)
	}
}
