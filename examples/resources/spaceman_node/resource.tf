resource "spaceman_node" "build" {
  name        = "build-01"
  description = "CI build host"
  labels      = { zone = "eu" }

  # Bump to issue a new enrollment token, for example when the host is rebuilt.
  enrollment_version = 1
}

# Hand the one-time token to the host's provisioning, which runs:
#   spaceman-agent enroll --token <token>
# The token works once and expires after nodes.enrollment_ttl (24 hours by default).
resource "local_sensitive_file" "build_enroll" {
  filename        = "${path.module}/build-01.enroll"
  content         = spaceman_node.build.enrollment_token
  file_permission = "0600"
}

resource "spaceman_space" "payments" {
  slug    = "payments"
  name    = "Payments"
  node_id = spaceman_node.build.id
}
