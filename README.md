# todo
An example app used to test pgpol read-write split

NOTE: I am using minikube to test this.

## Build docker image
```bash
export todo_version=1.7
docker build -t mjemala/todo:$(todo_version) .
```

### k8s deployment manifest
```yaml
kind: Deployment
apiVersion: apps/v1
metadata:
  name: todod
spec:
  selector:
    matchLabels:
      name: todod
  template:
    metadata:
      labels:
        name: todod
    spec:
      containers:
        - name: todod
          image: docker.io/mjemala/todo:1.7
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8080
              protocol: TCP
          env:
            - name: TODO_DSN
              value: "host=localhost user=postgres password=3OKF4pgPDb dbname=todo sslmode=disable"
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 2
            timeoutSeconds: 1
            periodSeconds: 2
            successThreshold: 1
            failureThreshold: 2
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 15
            timeoutSeconds: 1
            periodSeconds: 2
            successThreshold: 1
            failureThreshold: 2
          lifecycle:
            preStop:
              exec:
                command: [ "sh", "-c", "sleep 10" ]
          resources:
            requests:
              cpu: 50m
              memory: 64M

        - name: pgpool
          image: bitnami/pgpool:4
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 5432
              protocol: TCP
          env:
            - name: PGPOOL_BACKEND_NODES
              value: "0:refbdb-postgresql:5432:2:master:ALWAYS_MASTER|DISALLOW_TO_FAILOVER,1:refbdb-postgresql-read:5432:3:replica1|DISALLOW_TO_FAILOVER"
            - name: PGPOOL_POSTGRES_USERNAME
              value: "postgres"
            - name: PGPOOL_POSTGRES_PASSWORD
              value: "3OKF4pgPDb"
            - name: PGPOOL_SR_CHECK_PERIOD
              value: "0"
            - name: PGPOOL_HEALTH_CHECK_USER
              value: "postgres"
            - name: PGPOOL_HEALTH_CHECK_PASSWORD
              value: "3OKF4pgPDb"
            - name: PGPOOL_HEALTH_CHECK_PERIOD
              value: "5"
            - name: PGPOOL_HEALTH_CHECK_MAX_RETRIES
              value: "20"
            - name: PGPOOL_HEALTH_CHECK_RETRY_DELAY
              value: "1"
            - name: PGPOOL_NUM_INIT_CHILDREN
              value: "10"
            - name: PGPOOL_MAX_POOL
              value: "1"
            - name: PGPOOL_ENABLE_LOAD_BALANCING
              value: "yes"
            - name: PGPOOL_ENABLE_STATEMENT_LOAD_BALANCING
              value: "yes"
            - name: PGPOOL_ADMIN_USERNAME
              value: "iamnotused"
            - name: PGPOOL_ADMIN_PASSWORD
              value: "s3cret"
          readinessProbe:
            tcpSocket:
              port: 5432
            initialDelaySeconds: 5
            periodSeconds: 2
          livenessProbe:
            tcpSocket:
              port: 5432
            initialDelaySeconds: 10
            periodSeconds: 2
          lifecycle:
            preStop:
              exec:
                command: [ "sh", "-c", "sleep 10" ]
          resources:
            requests:
              cpu: 25m
              memory: 128M
```

*NOTE*: Play with the params, especially increase the `PGPOOL_NUM_INIT_CHILDREN` to see how it affects the availability during deployment restarts.

## Expose k8s service
```bash
kubectl expose deployment todod --type=NodePort --port=80
```

## Test zero-downtime deployment
Use this [gist](https://gist.github.com/michaljemala/df1d14804375033df1dcd4577dbba268).

*NOTE*: Use `minikube service todod` to get endpoint URL.
