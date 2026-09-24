package core

import _ "embed"

// These are the system prompts sent to the model. User prompts are assembled
// for each page snapshot and are built beside their planner call sites.
//
//go:embed prompts/planner.txt
var plannerSystem string

//go:embed prompts/repair.txt
var repairSystem string

//go:embed prompts/structure.txt
var structureSystem string
