# mysql-data-generator
Insert random data into MySQL.

## Available Options

```bash
❯ ./mysql-data-generator --help
Usage of ./mysql-data-generator:
  -concurrency int
        Number of parallel thread to inject data (default 1)
  -database string
        Name of the database to create (default "sampleData")
  -host string
        MySQL host address (default "localhost")
  -overwrite
        Drop previous database/table (if they exist) before inserting new one.
  -password string
        Password to use to connect with the database
  -port int
        Port number where the MySQL is listening (default 3306)
  -size string
        Size of the desired database (default "128MB")
  -tables int
        Number of tables to insert in the database (default 1)
  -user string
        Username to use to connect with the database
```

## Build

**Build Binary:**

```bash
go build .
```

**Build Docker Image:**

```bash
docker build -t emruzhossain/mysql-data-generator . \
&& docker push emruzhossain/mysql-data-generator
```


## Usage

**Run Locally:**

```bash
./mysql-data-generator --user=root --password='m$k&lzwjShBB0LhO' --size=5GB --concurrency=140
```
or

```bash
export USERNAME=<username>
export PASSWORD=<password>
./mysql-data-generator --size=5GB --concurrency=140 # make sure number of concurrency does not exceed "max_connections".
```

**Run Inside Kubernetes Cluster:**

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: msysql-data-generator
  namespace: demo
spec:
  backoffLimit: 0
  template:
    spec:
      containers:
        - name: generator
          image: emruzhossain/mysql-data-generator:latest
          imagePullPolicy: Always
          env:
            - name: USERNAME
              valueFrom:
                secretKeyRef:
                  name: my-group-auth
                  key: username
            - name: PASSWORD
              valueFrom:
                secretKeyRef:
                  name: my-group-auth
                  key: password
          args:
            - "--host=my-group.demo.svc"
            - "--port=3306"
            - "--size=5GB"
            - "--concurrency=1000"
            # - "--overwrite=true"
      restartPolicy: Never
```
```bash
kubectl apply -f ./data-generator.yaml
```
