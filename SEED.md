# Idea Seed

I want an overall personal dashboard. Truly personal, in that it coordinates data from services I use, and isn't intended for simple reusability. Instead, it is a central living dashboard and set of tools that allow me to view my life, project and work from different perspectives and with different foci.

Hence "facets".

## Facets

### Projects

The first facet we'll work on.

Should present each project I am working on, along with its open tasks, and analysis of recent work. This analysis should use codex, hermes, pi and omp sessions to show number of sessions by project by period.

Projects should be added manually via a cli tool "facet", which calls the backend facet.oregondevfoundry.com/api to do this via some auth key.

Tasks are managed via a plugin proxy system, allowing for a projects tasks to be managed by different systems-of-record, such as notion, linear, https://github.com/kenn-io/kata, or https://github.com/pengelbrecht/ticks

### Finances

### Goals

### Life

- Periodical event support, first class - for example should support "3 in 7 days" and "every day", and show "streaks" of days hitting the periodical goal.
