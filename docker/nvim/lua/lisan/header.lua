local M = {}

local glyphs = {
  [" "] = { "     ", "     ", "     ", "     ", "     " },
  ["-"] = { "     ", "     ", "#####", "     ", "     " },
  A = { " ### ", "#   #", "#####", "#   #", "#   #" },
  B = { "#### ", "#   #", "#### ", "#   #", "#### " },
  G = { " ### ", "#    ", "# ###", "#   #", " ### " },
  I = { "#####", "  #  ", "  #  ", "  #  ", "#####" },
  L = { "#    ", "#    ", "#    ", "#    ", "#####" },
  N = { "#   #", "##  #", "# # #", "#  ##", "#   #" },
  S = { "#####", "#    ", "#####", "    #", "#####" },
}

local function render(text)
  local lines = { "", "", "", "", "" }
  for letter in text:gmatch "." do
    for row = 1, #lines do
      lines[row] = lines[row] .. glyphs[letter][row] .. " "
    end
  end
  for row = 1, #lines do
    lines[row] = lines[row]:gsub("#", "█")
  end
  return lines
end

local function append(destination, source)
  for _, line in ipairs(source) do
    destination[#destination + 1] = line
  end
end

function M.lines()
  local width = vim.api.nvim_win_get_width(0)
  if width < 54 then
    return { "", "LISAN AL-GAIB", "", "Powered by NvChad", "" }
  end

  local lines = {}
  if width >= 90 then
    append(lines, render "LISAN AL-GAIB")
  else
    append(lines, render "LISAN")
    lines[#lines + 1] = ""
    append(lines, render "AL-GAIB")
  end
  lines[#lines + 1] = ""
  lines[#lines + 1] = "Powered by NvChad"
  lines[#lines + 1] = ""
  return lines
end

return M
