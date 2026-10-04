Feature: Feature flags
  Flags are served by flagd. These scenarios switch them by editing the tests'
  own copy of the flag definitions (integration/flagd/flags.json), which flagd
  reloads; the step waits until flagd serves the new value. The copy is put
  back to match flagd/flags.json after each scenario that changes it.

  Scenario: The list total is off as shipped
    When I GET "/items"
    Then the response status is 200
    And the JSON field "total" is absent

  Scenario: Switching the list total on
    Given the feature flag "items-list-total" is on
    When I GET "/items"
    Then the response status is 200
    And the JSON field "total" is 3
    And the JSON array "items" has 3 elements

  Scenario: Switching it back off
    Given the feature flag "items-list-total" is on
    And the feature flag "items-list-total" is off
    When I GET "/items"
    Then the JSON field "total" is absent
