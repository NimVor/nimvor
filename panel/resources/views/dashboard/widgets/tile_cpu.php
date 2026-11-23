<?php
/** @var \NimVor\View\View $this */
echo $this->partial('dashboard/tile', ['key' => 'cpu', 'label' => 'CPU', 'icon' => 'cpu', 'series' => 'cpu']);
