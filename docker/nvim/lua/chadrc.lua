---@type ChadrcConfig
local M = {}

M.base46 = {
  theme = "chocolate",
}

M.nvdash = {
  load_on_startup = true,
  header = function() return require("lisan.header").lines() end,
  buttons = {
    { txt = "󰱼  Open File", keys = "ff", cmd = "lua require('lisan.picker').open_file()" },
    { txt = function() return require("lisan.picker").workspace_label() end, keys = "fb", cmd = "lua require('lisan.picker').choose_workspace()" },
    { txt = "  Recent Files", keys = "fo", cmd = "Telescope oldfiles" },
    { txt = "󰈭  Find Word", keys = "fw", cmd = "Telescope live_grep" },
    { txt = "  Mappings", keys = "ch", cmd = "NvCheatsheet" },
  },
}

return M
