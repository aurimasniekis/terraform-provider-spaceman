# Built-in GitHub templates: github-issue, github-pull-request, github-actions, github-pr-comment,
# github-issue-comment, github-pr-review, github-pr-review-comment, github-workflow-run, github-check-suite.
data "spaceman_event_template" "github_issues" {
  slug = "github-issue"
}

data "spaceman_event_template" "github_workflow_runs" {
  slug = "github-workflow-run"
}

# The latest version's variables and filters (including optional `if_set` / `unless_set` filters).
output "github_workflow_run_variables" {
  value = [for v in jsondecode(data.spaceman_event_template.github_workflow_runs.definition).variables : v.name]
}
