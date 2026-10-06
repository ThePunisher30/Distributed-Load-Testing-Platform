# Kubernetes deployment

Runs the platform on a local Kubernetes cluster (Docker Desktop's built-in
Kubernetes, or kind/minikube) instead of Docker Compose. Each Compose service
becomes a Deployment + Service; the worker pool autoscales via an HPA; and
Prometheus + Grafana run in-cluster so the dashboards see the k8s workload.

## Prerequisites

- A running cluster: `kubectl get nodes` should succeed. (Docker Desktop →
  Settings → Kubernetes → Enable.)
- The images built locally (Docker Desktop's k8s shares the Docker image store, so
  no registry is needed — the manifests use `imagePullPolicy: IfNotPresent`):

  ```bash
  docker compose build backend worker target frontend
  ```

## Deploy

```bash
# 1. Namespace first.
kubectl apply -f k8s/00-namespace.yaml

# 2. Bootstrap the schema: the migrations become a ConfigMap that Postgres runs
#    from /docker-entrypoint-initdb.d on first start (same as Compose).
kubectl -n dltp create configmap dltp-migrations --from-file=migrations/

# 3. Monitoring config/provisioning as ConfigMaps (reusing the Compose files).
kubectl -n dltp create configmap prometheus-config --from-file=prometheus.yml=monitoring/prometheus.yml
kubectl -n dltp create configmap grafana-datasource --from-file=monitoring/grafana/provisioning/datasources/prometheus.yml
kubectl -n dltp create configmap grafana-dashboards-provider --from-file=monitoring/grafana/provisioning/dashboards/dashboards.yml
kubectl -n dltp create configmap grafana-dashboard-json --from-file=monitoring/grafana/dashboards/loadtest.json

# 4. Everything else.
kubectl apply -f k8s/

# 4. Watch it come up.
kubectl -n dltp get pods -w
```

## Autoscaling (HPA)

The worker HPA needs **metrics-server** (not installed in Docker Desktop k8s by
default):

```bash
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
# Docker Desktop's kubelet serves metrics over self-signed TLS, so allow that:
kubectl -n kube-system patch deployment metrics-server --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
```

Then watch the pool scale under load:

```bash
kubectl -n dltp get hpa,pods -w     # in one terminal
```

## Access

```bash
kubectl -n dltp port-forward svc/frontend 3002:80   # dashboard -> http://localhost:3002
kubectl -n dltp port-forward svc/backend 18080:8080  # API       -> http://localhost:18080
kubectl -n dltp port-forward svc/grafana 3003:3000   # Grafana   -> http://localhost:3003
```

(Ports chosen to not clash with the Compose stack's 3001/8080/3000.)

## Tear down

```bash
kubectl delete namespace dltp
```
