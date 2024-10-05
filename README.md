# EVE ♥ LHK

Q&D Script to apply [LKH solver](http://webhotel4.ruc.dk/~keld/research/LKH-3/) to [EVE online](https://www.eveonline.com/).

Current features:
- Download the starmap information from EVE's API into a `.json` file.
- Generate full matrix for the K-space EVE graph.
- Generate LKH `.tsp` files.
- Filter by logs (remove systems you've already visited).
- Filter by region.
- Upload LKH `.tour` solution to EVE's route using the SSO API.

Todo:
- Let the script run LKH itself.
- WASM build usable in the browser with an in-browser UI.
- Advanced routing
  - Optimize any arbitrary paths, not just all systems you havn't visited yet.
    In other words, make it identical to the « optimize route » feature in game, but able to handle all 5201 systems if you wanted to.
  - Integrate market and contracts API with constrained LKH solvers,
    In other words, let LKH solve for profitable market abitrage and courier contract multi-stops paths.
- Combinable search parameters,
  what if you find the shortest reactions available station at most 2 jumps away from highsec with one query ?
- Wormhole pathfinding.