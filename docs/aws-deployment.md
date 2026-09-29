# Existing AWS application deployment

[Back to the troubleshooting lab](../README.md) · [Remote monitoring example](monitoring/prometheus-grafana.md)

This is an older, environment-specific **application deployment** path. It is separate from the reported Ubuntu/k3s monitoring instance. The CI workflow builds and scans the image, pushes an immutable digest to ECR, and invokes SSM to deploy the API, worker, and PostgreSQL on one EC2 host. That host runs the application with Docker Compose and binds its API to `127.0.0.1:8080` for private SSM port forwarding. It does not install k3s, Prometheus, Grafana, or Alertmanager.

The checked-in `deploy/aws` scripts, EC2 image Compose file, and CI workflow contain account, region, instance, image, and IAM assumptions for their original environment. Read and adapt those source files before using them in another AWS account. This page does not provide a one-line command that would accidentally target that existing environment.

The deployment sequence in [the host script](../deploy/ec2/image/host-deploy.sh) is: pull the pinned image digest, start PostgreSQL, apply Goose migrations, seed only fictional demo data, start API and worker, then check `/readyz`. The bootstrap script creates a local random database password; the runtime file is kept on the EC2 host. The API is reached through an authenticated SSM port-forwarding session to the instance, using your own AWS profile, region, and instance ID.

This remains a private demo. The EC2 Compose PostgreSQL has no published host port. The stack needs its own operational review before any public deployment or real customer data. See [security operations](security.md) and the [historical delivery report](delivery.md) for the implementation status at the time of the earlier audit.
