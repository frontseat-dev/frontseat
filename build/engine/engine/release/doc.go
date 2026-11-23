// Package release says what releasing entities would do: how each
// entity's settings resolve, the units a request releases, and for each
// unit its next version and why. It reads the catalog and what entities
// say of how they are released, asks a repository's history through a
// port and the strategies through another, and changes nothing.
//
// A unit is what gets one version and one tag: a domain that says its
// systems version together, a system whose members version together, or
// an entity that versions alone. Its members are in one repository. Its
// settings are inherited, a domain's by its systems and a system's by its
// members, the nearest level that says a key winning, key by key; a
// member of a synced group, a system of a synced domain among them, says
// none but its grouping, and a system there says independent or
// nothing. A domain named that is no synced group is released as each of
// its systems would be. Versions never go backwards: a unit with no
// release of its own continues above what its members, their systems or
// their domain were released as. Every system and domain a released
// entity is part of is held by the workspace, or planning is refused.
//
// Served: a component alone, a system and a domain as a synced group;
// Release Flow and conventional commits through the strategies a
// repository lists; a branch's line; a first release; a unit that holds.
// Not served, and refused by name: a domain within a domain (Backstage's
// subdomainOf). Not yet held: a unit is not refused against
// an unreleased change of a unit it depends on.
package release
