# AWS EC2 CI/CD

This demo pipeline targets AWS account `740089361110`, region `us-east-1`, and EC2
instance `i-086da8aaafa2b2683`.

On every push to `main`, GitHub Actions first completes formatting, tests, race tests,
database migration checks, Go security scans, the container scan, and DAST. It then uses
GitHub OIDC to assume a short-lived AWS role, builds and rescans the exact commit image,
pushes it to private ECR, resolves its immutable digest, and sends that digest to the EC2
instance through SSM. No AWS access keys or EC2 private key are stored in GitHub.

One-time AWS resources are described in `deploy/aws/infrastructure.yml`:

```sh
AWS_PROFILE=hr-portal-friend AWS_REGION=us-east-1 bash deploy/aws/provision.sh
```

One-time host setup installs Docker Compose and SSM Agent, then creates a random local demo
database password. Copy `deploy/ec2` to the instance and run:

```sh
sudo /tmp/airline-bootstrap/bootstrap-host.sh
```

The deployment starts PostgreSQL, applies Goose migrations, loads only fictional demo data,
and starts the API and worker. The API listens only on `127.0.0.1:8080`; the existing Apache
site is not changed. Access it privately using an SSM port-forwarding session:

```sh
aws ssm start-session \
  --profile hr-portal-friend \
  --region us-east-1 \
  --target i-086da8aaafa2b2683 \
  --document-name AWS-StartPortForwardingSession \
  --parameters '{"portNumber":["8080"],"localPortNumber":["8080"]}'
```

Then open `http://127.0.0.1:8080` locally.

This is a private demo deployment. PostgreSQL uses the instance's existing unencrypted root
disk and local container networking, so never enter real customer, payment, or travel-document
data. A public production launch still needs encrypted database storage, verified TLS, backups,
and HTTPS ingress.
