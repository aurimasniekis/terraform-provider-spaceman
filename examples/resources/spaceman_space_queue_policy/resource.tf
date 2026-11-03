resource "spaceman_space_queue_policy" "payments" {
  space_id            = spaceman_space.payments.id
  max_top_level_tasks = 2
  incident_insertion  = "top"
  incident_preempts   = true

  # Who may raise incidents: operators, and events rendered by the PagerDuty template. Control's own incident
  # requests (and anything else) become proposals an operator confirms.
  incident_triggers  = ["operator", "template:pagerduty-incident"]
  incident_untrusted = "propose"
  # Pause the preempted task's workers and resume them when the incident ends.
  incident_workers = "pause"
  incident_resume  = "auto"
}
