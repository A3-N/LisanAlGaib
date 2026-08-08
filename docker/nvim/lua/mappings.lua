require "nvchad.mappings"

local map = vim.keymap.set
map("n", ";", ":", { desc = "Command mode" })
map("i", "jk", "<Esc>")
